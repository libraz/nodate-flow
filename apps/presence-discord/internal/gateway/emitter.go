// HTTP client that translates debounced presence events into POST
// /signals calls against flow-api. The Discord snowflake → flow user
// resolution goes through a separate lookup endpoint rather than a
// direct DB read, so this binary stays DB-free.
//
// Resolution flow:
//
//	GET  {FlowAPIBaseURL}/internal/users/by-discord/{snowflake}
//	     → 200 { userId, workspaceId }   → POST /signals
//	     → 404                            → drop_no_user
//	     → other                          → signal_failed (no retry)
//
//	POST {FlowAPIBaseURL}/signals
//	     Body: discord.presence signal envelope (see SignalKind)
//	     → 2xx → signal_emitted
//	     → non-2xx → signal_failed (no retry; the next presence
//	       transition supersedes this one anyway)
//
// Retries are intentionally absent: presence is supersede-by-design.
// A failed emit is replaced by the next gateway event for the same
// user, and the leading-edge debounce ensures the new attempt fires
// promptly.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/presence-discord/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/signalwire"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// SignalKind is the wire-level signal kind emitted for every
// PresenceUpdate. Matches signal_kinds/presence.yaml entry
// "discord.presence".
const SignalKind = "discord.presence"

// SignalSource is the wire-level source enum value reported on POST
// /signals. Derived from packages/go-shared/signalwire (the canonical
// signals.source enum) so this value can never drift from what flow-api's
// Huma enum / DB ENUM accept.
const SignalSource = string(signalwire.SourceDiscord)

// SignalSubjectType is the subject_type the worker always emits.
// Discord presence is fundamentally about a user, so every signal
// row is subject_type='user' with subject_id = the resolved flow user
// public id (UUID v7).
const SignalSubjectType = "user"

// maxLookupBodyLen caps how much of the by-discord lookup response is
// read into memory. The client has a timeout, so an endless body is
// bounded in time already, but a fast one is not bounded in size: the
// decoder would keep allocating for as long as bytes arrive. The two
// fields below are a few dozen bytes; 1 MiB leaves room for an error
// envelope and nothing else.
const maxLookupBodyLen = 1 << 20

// resolverResponse is the JSON shape returned by the by-discord lookup
// endpoint. Mirrored field-for-field on the flow-api side; any
// divergence is a contract bug.
type resolverResponse struct {
	UserID      string `json:"userId"`
	WorkspaceID string `json:"workspaceId"`
}

// signalCreateBody is the POST /signals wire body. It aliases
// signalwire.CreateRequest — the single shared wire shape owned by
// packages/go-shared/signalwire — so presence-discord, flow-worker, and
// flow-api can never diverge on field names or casing. Importing only
// go-shared (not flow-api) keeps this binary free of flow-api's sqlc /
// generated-model dependency graph.
type signalCreateBody = signalwire.CreateRequest

// signalPayload is the JSON object stored as signals.payload_json for
// a discord.presence row. Schema mirrors the YAML in
// signal_kinds/presence.yaml; the judge prompt reads these fields.
type signalPayload struct {
	Status           string `json:"status"`
	GuildID          string `json:"guildId,omitempty"`
	GatewaySessionID string `json:"gatewaySessionId,omitempty"`
	Activities       []any  `json:"activities,omitempty"`
}

// httpDoer narrows *http.Client to the surface the emitter uses.
// Tests inject a stub to assert on requests without binding a real
// listener.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Emitter is the HTTP-backed presence-signal sender. One instance per
// gateway; safe for concurrent use because the underlying http.Client
// is.
type Emitter struct {
	baseURL     string
	signalToken string
	httpClient  httpDoer
	logger      *slog.Logger
}

// EmitterConfig collects construction-time inputs. The signalToken is
// presented as Authorization: Bearer on both the lookup GET and the
// emit POST; flow-api's signals middleware accepts the same shared
// secret on both paths (one config knob, two consumers).
type EmitterConfig struct {
	BaseURL     string
	SignalToken string
	HTTPClient  httpDoer
	Logger      *slog.Logger
}

// NewEmitter constructs an Emitter. A nil HTTPClient is replaced with
// a tuned default (10s timeout, no idle pooling because we POST < 1/s
// per process and pool-warming is wasted churn).
func NewEmitter(cfg EmitterConfig) *Emitter {
	doer := cfg.HTTPClient
	if doer == nil {
		doer = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Emitter{
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		signalToken: cfg.SignalToken,
		httpClient:  doer,
		logger:      logger,
	}
}

// Emit resolves the Discord snowflake to a flow user and posts the
// presence signal. Errors are logged + metric-counted; they never
// propagate because there is no upstream caller to surface them to —
// the discordgo handler that pushed this event has already returned.
func (e *Emitter) Emit(ctx context.Context, ev PresenceEvent) {
	if e.signalToken == "" {
		// Boot-time misconfiguration. Log once per event so the
		// operator notices, but don't spin — there's no recovery the
		// worker can do here.
		e.logger.WarnContext(ctx, "presence-discord: signal token unset; dropping event",
			slog.String("user_snowflake", ev.UserID),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return
	}

	resolved, ok := e.resolveUser(ctx, ev.UserID)
	if !ok {
		return
	}

	if err := e.postSignal(ctx, resolved, ev); err != nil {
		e.logger.WarnContext(ctx, "presence-discord: signal emit failed",
			slog.Any("err", err),
			slog.String("workspace_id", resolved.WorkspaceID),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return
	}
	obs.EventsTotal.WithLabelValues("signal_emitted").Inc()
}

// resolveUser calls the by-discord lookup endpoint. Returns (resolved,
// true) on 200, (zero, false) on every other outcome. Metrics are
// incremented inside this function so the caller never has to think
// about which bucket a failure lands in.
func (e *Emitter) resolveUser(ctx context.Context, snowflake string) (resolverResponse, bool) {
	u, err := buildLookupURL(e.baseURL, snowflake)
	if err != nil {
		e.logger.WarnContext(ctx, "presence-discord: lookup url build failed",
			slog.Any("err", err),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		e.logger.WarnContext(ctx, "presence-discord: lookup request build failed",
			slog.Any("err", err),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}
	req.Header.Set("Authorization", "Bearer "+e.signalToken)
	req.Header.Set("Accept", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.logger.WarnContext(ctx, "presence-discord: lookup HTTP error",
			slog.Any("err", err),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Discord user not connected to any flow account. Expected and
		// noisy; emit at debug so default-level logs stay clean.
		e.logger.DebugContext(ctx, "presence-discord: snowflake not bound to any flow user")
		obs.EventsTotal.WithLabelValues("drop_no_user").Inc()
		return resolverResponse{}, false
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// Fall through to decode.
	default:
		body := truncatedBody(resp.Body)
		e.logger.WarnContext(ctx, "presence-discord: lookup non-2xx",
			slog.Int("status", resp.StatusCode),
			slog.String("body", body),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}

	var out resolverResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxLookupBodyLen)).Decode(&out); err != nil {
		e.logger.WarnContext(ctx, "presence-discord: lookup decode failed",
			slog.Any("err", err),
		)
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}
	if out.UserID == "" || out.WorkspaceID == "" {
		e.logger.WarnContext(ctx, "presence-discord: lookup returned empty ids")
		obs.EventsTotal.WithLabelValues("signal_failed").Inc()
		return resolverResponse{}, false
	}
	return out, true
}

// postSignal serialises the presence event and POSTs /signals. Returns
// nil on 2xx, an error describing the failure mode otherwise.
func (e *Emitter) postSignal(ctx context.Context, resolved resolverResponse, ev PresenceEvent) error {
	payload, err := json.Marshal(signalPayload{
		Status:           ev.Status,
		GuildID:          ev.GuildID,
		GatewaySessionID: ev.GatewaySessionID,
		Activities:       ev.Activities,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	body, err := json.Marshal(signalCreateBody{
		WorkspaceID: resolved.WorkspaceID,
		Source:      SignalSource,
		Kind:        SignalKind,
		SubjectType: SignalSubjectType,
		SubjectID:   resolved.UserID,
		Payload:     payload,
		// ExpiresAt nil: the next presence transition supersedes this
		// signal, so it never expires by clock.
	})
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	url := e.baseURL + "/signals"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.signalToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return fmt.Errorf("flow-api responded %d: %s", resp.StatusCode, truncatedBody(resp.Body))
}

// buildLookupURL composes the lookup endpoint URL with proper path
// escaping for the snowflake (which is numeric in practice, but the
// escape costs nothing and guards against future format changes).
func buildLookupURL(baseURL, snowflake string) (string, error) {
	if baseURL == "" {
		return "", errors.New("base url is empty")
	}
	if snowflake == "" {
		return "", errors.New("snowflake is empty")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	u.Path = path.Join(u.Path, "internal", "users", "by-discord", url.PathEscape(snowflake))
	return u.String(), nil
}

// truncatedBody reads at most 512 bytes from the response body so log
// lines stay bounded even when flow-api returns a long error envelope.
// Reading inside the defer-Close above is safe because the next read
// after truncation closes the underlying connection.
//
// The clip lands on a rune boundary: an error envelope carries the
// offending values, which are workspace content and need not be ASCII,
// and a byte cut would put U+FFFD at the end of the log line.
func truncatedBody(r io.Reader) string {
	const max = 512
	buf := make([]byte, max+1)
	n, _ := io.ReadFull(r, buf)
	if n > max {
		return stringutil.TruncateBytes(string(buf[:n]), max) + "...(truncated)"
	}
	return string(buf[:n])
}
