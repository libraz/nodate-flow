// Package gateway hosts the Discord gateway WebSocket bridge that
// translates presence updates into signals POSTed to flow-api.
//
// P8-2 fills in the discordgo wiring, the per-user debounce table,
// and the HTTP emitter that calls POST /signals (with a separate
// lookup against GET /internal/users/by-discord/{snowflake} to map a
// Discord snowflake to a flow user public id).
//
// The shape of this package is deliberately tight:
//
//   - New() takes only the dependencies the lifecycle owns (cfg,
//     logger). Discord-specific state (session, debouncer, HTTP
//     emitter) is constructed in Start so cmd/gateway can keep its
//     thin shape.
//   - Start blocks until ctx is cancelled or the discordgo session
//     fails to open. The lifecycle's gateway goroutine surfaces a
//     non-nil error as a fatal exit.
//   - Stop is idempotent and safe to call from a deferred path; it
//     drains pending debounce timers and closes the discordgo
//     session.
//
// Reconnection is delegated to discordgo entirely: the library handles
// the WS heartbeat / re-identify cycle internally and re-emits
// PresenceUpdate snapshots on resume. The Disconnect / Resumed event
// handlers toggle GatewayUp so dashboards see the gauge dip during
// re-identify windows.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/obs"
)

// sessionAdapter is the narrow surface Gateway needs from a
// discordgo.Session. Tests inject a fake to drive Start / Stop
// without opening a real WS.
type sessionAdapter interface {
	Open() error
	Close() error
	AddHandler(handler interface{}) func()
}

// eventSink is the trim surface the discordgo handler pushes into. The
// production wiring satisfies this with *Debouncer; tests can
// substitute an in-process recorder.
type eventSink interface {
	Handle(PresenceEvent)
}

// Gateway is the Discord WS bridge. The bot token, debounce table, and
// signal emission HTTP client live here.
type Gateway struct {
	cfg    *config.Config
	logger *slog.Logger

	mu      sync.Mutex
	started bool
	stopped bool

	session   sessionAdapter
	debouncer *Debouncer
	emitter   *Emitter
	sink      eventSink

	// removeHandlers is the cleanup func returned by AddHandler. Called
	// during Stop so the discordgo dispatch loop stops invoking our
	// handler after the session closes.
	removeHandlers []func()

	// newSession is overridable in tests. nil in production; when nil
	// Start uses discordgo.New("Bot "+token).
	newSession func(token string) (sessionAdapter, error)
}

// New constructs a Gateway bound to the supplied configuration and
// logger. The constructor does NOT open the Discord connection; that
// happens in Start so lifecycle.Run can sequence the gateway boot
// against the metrics endpoint coming up first.
//
// logger is expected to already carry the service / version attributes
// installed by lifecycle.NewLogger.
func New(cfg *config.Config, logger *slog.Logger) *Gateway {
	return &Gateway{
		cfg:    cfg,
		logger: logger,
	}
}

// Start opens the gateway connection and begins consuming presence
// events. Blocks until ctx is cancelled or a fatal error occurs (the
// discordgo session failing to open).
//
// The boot sequence is:
//  1. Validate the bot and signal tokens. An empty token is an operator
//     misconfiguration, not a fatal error: the gateway logs, sets
//     GatewayUp to 0, and blocks on ctx.Done() so the process stays up
//     and /metrics stays scrapable for alerting (see config.Config doc).
//  2. Construct the HTTP emitter and the debouncer (the emitter
//     receives debounced events from the debouncer).
//  3. Open the discordgo session with the GUILD_PRESENCES +
//     GUILD_MEMBERS intents (both privileged; see
//     docs/conventions/discord-bot-setup.md for the developer-portal
//     opt-in steps).
//  4. Register the PresenceUpdate handler plus Connect / Disconnect /
//     Resumed handlers that keep GatewayUp accurate.
//  5. Block on ctx.Done().
func (g *Gateway) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return errors.New("gateway: Start called twice")
	}
	g.started = true
	g.mu.Unlock()

	// Missing credentials are an operator misconfiguration, not a reason
	// to crash-loop the container. The config docstring promises the
	// metrics endpoint stays scrapable so alerting can fire on
	// nf_presence_discord_gateway_up=0; honour that by logging, parking
	// the gauge at 0, and blocking on ctx.Done() instead of exiting. The
	// process stays up (and scrapable) until the operator sets the
	// credential and restarts.
	if g.cfg.DiscordBotToken == "" {
		obs.GatewayUp.Set(0)
		g.logger.Error("presence-discord gateway not starting: NF_DISCORD_BOT_TOKEN is empty; staying up with gateway_up=0 so /metrics remains scrapable")
		<-ctx.Done()
		return nil
	}
	if g.cfg.FlowAPISignalToken == "" {
		obs.GatewayUp.Set(0)
		g.logger.Error("presence-discord gateway not starting: NF_FLOW_API_SIGNAL_TOKEN is empty; staying up with gateway_up=0 so /metrics remains scrapable")
		<-ctx.Done()
		return nil
	}

	emitter := NewEmitter(EmitterConfig{
		BaseURL:     g.cfg.FlowAPIBaseURL,
		SignalToken: g.cfg.FlowAPISignalToken,
		Logger:      g.logger,
	})
	g.emitter = emitter

	debouncer := NewDebouncer(ctx, time.Duration(g.cfg.DebounceSeconds)*time.Second, emitter)
	g.debouncer = debouncer
	if g.sink == nil {
		g.sink = debouncer
	}

	session, err := g.openSession()
	if err != nil {
		obs.GatewayUp.Set(0)
		return err
	}
	g.session = session

	obs.GatewayUp.Set(1)
	g.logger.Info("presence-discord gateway connected",
		slog.String("flow_api_base_url", g.cfg.FlowAPIBaseURL),
		slog.Int("debounce_seconds", g.cfg.DebounceSeconds),
	)

	<-ctx.Done()
	return nil
}

// openSession instantiates and Opens the discordgo session, attaching
// every handler before Open so the very first PresenceUpdate after
// READY is captured. Failure to Open is fatal — discordgo only fails
// here on auth / intent errors, both of which require operator
// intervention.
func (g *Gateway) openSession() (sessionAdapter, error) {
	factory := g.newSession
	if factory == nil {
		factory = defaultSessionFactory
	}
	session, err := factory(g.cfg.DiscordBotToken)
	if err != nil {
		return nil, err
	}

	// Attach handlers BEFORE Open so the burst of PresenceUpdate
	// events on READY (Discord emits a full snapshot per guild
	// member) is captured.
	g.removeHandlers = append(g.removeHandlers,
		session.AddHandler(g.onPresenceUpdate),
		session.AddHandler(g.onReady),
		session.AddHandler(g.onResumed),
		session.AddHandler(g.onDisconnect),
	)

	if err := session.Open(); err != nil {
		// Drop the handlers we just attached so a retry from cmd/gateway
		// (operator restart) does not double-register.
		for _, remove := range g.removeHandlers {
			if remove != nil {
				remove()
			}
		}
		g.removeHandlers = nil
		return nil, err
	}
	return session, nil
}

// defaultSessionFactory is the production wiring: discordgo.New with
// the GUILD_PRESENCES + GUILD_MEMBERS intents. Both are privileged and
// must be opted-in via the Discord Developer Portal; see
// docs/conventions/discord-bot-setup.md.
func defaultSessionFactory(token string) (sessionAdapter, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuildPresences | discordgo.IntentsGuildMembers
	s.SyncEvents = true
	// Keep State enabled (the default) so State.SessionID is
	// populated for PresenceUpdate correlation. The cache cost is
	// modest for a presence-only bot — we are not tracking messages
	// or members beyond what the gateway already streams.
	return discordgoAdapter{s}, nil
}

// discordgoAdapter wires *discordgo.Session into sessionAdapter. The
// type exists solely to make the production session interchangeable
// with the test fake without leaking discordgo into Gateway's tests.
type discordgoAdapter struct {
	*discordgo.Session
}

func (a discordgoAdapter) Open() error  { return a.Session.Open() }
func (a discordgoAdapter) Close() error { return a.Session.Close() }
func (a discordgoAdapter) AddHandler(h interface{}) func() {
	return a.Session.AddHandler(h)
}

// onPresenceUpdate is the hot-path discordgo handler. discordgo
// dispatches every PresenceUpdate event to this function on its event
// goroutine; we translate the discordgo struct into a PresenceEvent
// and push it into the debouncer.
//
// The activities slice is copied into a slice of `any` so the
// debouncer holds no references into discordgo's internal pools. The
// session's State.SessionID is captured for debug correlation; it is
// stable across resumes within the same WS session and rotates on a
// full re-identify, which is exactly the signal operators want when
// tracing "did this presence event arrive over the same connection?".
func (g *Gateway) onPresenceUpdate(s *discordgo.Session, pu *discordgo.PresenceUpdate) {
	if pu == nil || pu.User == nil {
		return
	}
	obs.EventsTotal.WithLabelValues("presence_update").Inc()

	activities := make([]any, 0, len(pu.Activities))
	for _, act := range pu.Activities {
		if act == nil {
			continue
		}
		// Marshal-friendly subset: name, type, details, state, url.
		// The judge only needs the human-readable label fields; raw
		// presence flags add cardinality without value.
		activities = append(activities, map[string]any{
			"name":    act.Name,
			"type":    int(act.Type),
			"details": act.Details,
			"state":   act.State,
			"url":     act.URL,
		})
	}

	sessionID := ""
	if s != nil && s.State != nil {
		// State.SessionID is populated after READY by discordgo's
		// onReady hook. Nil-guarded so unit tests can fire
		// PresenceUpdate against a zero-value *discordgo.Session
		// without panicking.
		sessionID = s.State.SessionID
	}

	ev := PresenceEvent{
		UserID:           pu.User.ID,
		Status:           string(pu.Status),
		GuildID:          pu.GuildID,
		GatewaySessionID: sessionID,
		Activities:       activities,
		ReceivedAt:       time.Now(),
	}
	if g.sink != nil {
		g.sink.Handle(ev)
	}
}

// onReady fires after discordgo has identified with the gateway and
// completed the initial guild sync. Treated as the "fully connected"
// marker; PresenceUpdate bursts arriving before this still flow
// because the handler is registered up front.
func (g *Gateway) onReady(_ *discordgo.Session, _ *discordgo.Ready) {
	obs.GatewayUp.Set(1)
	g.logger.Info("presence-discord gateway ready")
}

// onResumed fires when discordgo successfully re-identifies after a
// transient disconnect. The gauge bounces back to 1 here even though
// Discord did not emit a fresh READY.
func (g *Gateway) onResumed(_ *discordgo.Session, _ *discordgo.Resumed) {
	obs.GatewayUp.Set(1)
	g.logger.Info("presence-discord gateway resumed")
}

// onDisconnect fires whenever the WS link goes down. discordgo
// reconnects on its own; we only flip the gauge so dashboards see the
// gap.
func (g *Gateway) onDisconnect(_ *discordgo.Session, _ *discordgo.Disconnect) {
	obs.GatewayUp.Set(0)
	g.logger.Warn("presence-discord gateway disconnected; discordgo will reconnect")
}

// Stop cleanly disconnects the gateway. Idempotent: calling Stop before
// Start, or twice, is a no-op.
//
// Stop drains pending debounce timers (without firing them — the next
// session re-emits presence snapshots), removes the discordgo
// handlers, and closes the WS session.
func (g *Gateway) Stop(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil
	}
	g.stopped = true

	if g.debouncer != nil {
		g.debouncer.Stop()
	}
	for _, remove := range g.removeHandlers {
		if remove != nil {
			remove()
		}
	}
	g.removeHandlers = nil

	if g.session != nil {
		if err := g.session.Close(); err != nil {
			g.logger.Warn("presence-discord session close failed",
				slog.Any("err", err),
			)
		}
		g.session = nil
	}

	obs.GatewayUp.Set(0)
	g.logger.Info("presence-discord gateway stopped")
	return nil
}
