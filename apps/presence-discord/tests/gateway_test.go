// Integration tests for the presence-discord gateway. Phase 8 / P8-5
// of docs/plan/release-8-signals-and-judge-loop.md.
//
// Scope:
//
//   - happy path: a single PresenceUpdate produces exactly one
//     POST /signals after the by-discord lookup succeeds,
//   - debounce burst: three PresenceUpdate events within the window
//     collapse to at most two emits (leading + trailing) and the
//     debounce-dropped counter increments,
//   - unknown user drop: a 404 from the lookup endpoint suppresses the
//     downstream POST and increments the drop_no_user counter,
//   - reconnect probe: Stop-then-Start re-invokes the discordgo session
//     factory, demonstrating that the gateway is reusable after a
//     clean shutdown (a proxy for discordgo's own auto-reconnect, which
//     is provider-library behaviour not worker logic).
//
// The suite deliberately avoids:
//
//   - testcontainers MySQL — the worker has no DB connection of its own
//     (P8-1 decision); the fake flow-api below is the entire data plane,
//   - a real Discord WS — synthetic PresenceUpdate events are dispatched
//     through the gateway's test seam (apps/presence-discord/internal/
//     gateway/testseam.go), which feeds the production handler chain
//     (onPresenceUpdate -> debouncer -> emitter) with no transport.
//
// Metrics assertions diff the global Prometheus counters from before to
// after each test, so the tests can run in any order without resetting
// the registry. The debouncer and emitter run on real goroutines, so
// trailing-emit assertions use require.Eventually rather than fixed
// sleeps.
package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/gateway"
	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/obs"
)

// signalTokenFixture is the shared bearer presented on both the lookup
// GET and the emit POST. Arbitrary but stable; the fake flow-api
// asserts an exact match so a worker that forgot to set the header
// fails fast.
const signalTokenFixture = "test-presence-signal-token-0123456789abcdef0123456789abcdef0123"

// fakeFlowAPI is the test-double for flow-api: serves the by-discord
// resolver on GET /internal/users/by-discord/{snowflake} and records
// every POST /signals it receives. The lookup table is keyed by the
// Discord snowflake; an entry of {} (zero value) signals "respond 404".
type fakeFlowAPI struct {
	server *httptest.Server

	mu         sync.Mutex
	lookups    map[string]resolverFixture // snowflake -> response, nil = 404
	signals    []capturedSignal
	t          *testing.T
	lookupHits atomic.Int32
	signalHits atomic.Int32
}

// resolverFixture is the (userId, workspaceId) pair the fake flow-api
// returns from a successful lookup. Both fields use UUID-shaped
// fixtures so the worker's "empty id" guard does not skip the post.
type resolverFixture struct {
	UserID      string
	WorkspaceID string
}

// capturedSignal records a single POST /signals invocation for later
// assertion. Body is parsed JSON; raw bytes are stored too so tests
// can verify field shapes the worker depends on.
type capturedSignal struct {
	authorization string
	contentType   string
	rawBody       []byte
	parsed        capturedSignalBody
}

// capturedSignalBody mirrors the wire shape of signalCreateBody on the
// worker side. Repeated here (rather than imported) because the worker
// type is unexported and the integration test should pin the wire
// contract independently.
type capturedSignalBody struct {
	WorkspaceID string          `json:"workspaceId"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	SubjectType string          `json:"subjectType"`
	SubjectID   string          `json:"subjectId"`
	Payload     json.RawMessage `json:"payload"`
}

// newFakeFlowAPI starts a httptest.Server with two routes:
//
//   - GET /internal/users/by-discord/{snowflake} — looks up the snowflake
//     in f.lookups, responding 200 + JSON or 404,
//   - POST /signals — records the request and responds 201.
//
// Both routes assert that the Authorization header is the expected
// bearer; a missing or wrong token fails the test immediately via the
// captured *testing.T.
func newFakeFlowAPI(t *testing.T) *fakeFlowAPI {
	t.Helper()
	f := &fakeFlowAPI{
		lookups: make(map[string]resolverFixture),
		t:       t,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/users/by-discord/", f.handleLookup)
	mux.HandleFunc("/signals", f.handleSignal)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// SetLookup registers a 200 response for a given snowflake. Snowflakes
// not registered respond 404.
func (f *fakeFlowAPI) SetLookup(snowflake, userID, workspaceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups[snowflake] = resolverFixture{UserID: userID, WorkspaceID: workspaceID}
}

// BaseURL is the URL to point the gateway at. Strips the trailing
// slash so the emitter's TrimRight is a no-op.
func (f *fakeFlowAPI) BaseURL() string {
	return strings.TrimRight(f.server.URL, "/")
}

// Signals returns a snapshot of every recorded POST /signals body.
// Safe for concurrent use; the caller receives a fresh slice.
func (f *fakeFlowAPI) Signals() []capturedSignal {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedSignal, len(f.signals))
	copy(out, f.signals)
	return out
}

// SignalCount is the count-only accessor used by Eventually loops to
// avoid taking the lock + allocating on every probe iteration.
func (f *fakeFlowAPI) SignalCount() int {
	return int(f.signalHits.Load())
}

// LookupCount is the count of by-discord lookup hits, useful for
// asserting "lookup occurred but emit was suppressed" in the drop
// scenario.
func (f *fakeFlowAPI) LookupCount() int {
	return int(f.lookupHits.Load())
}

// handleLookup is the GET /internal/users/by-discord/{snowflake}
// handler. Extracts the snowflake from the path tail, asserts the
// auth header, and responds based on the registered fixture (or 404).
func (f *fakeFlowAPI) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+signalTokenFixture {
		// Surface the mismatch through t.Errorf rather than 401 so the
		// test fails with a precise message rather than a generic
		// "lookup failed".
		f.t.Errorf("fake flow-api: lookup Authorization header mismatch: got %q", got)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	prefix := "/internal/users/by-discord/"
	snowflake := strings.TrimPrefix(r.URL.Path, prefix)
	if snowflake == "" || snowflake == r.URL.Path {
		http.Error(w, "missing snowflake", http.StatusBadRequest)
		return
	}
	f.lookupHits.Add(1)

	f.mu.Lock()
	fixture, ok := f.lookups[snowflake]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"userId":      fixture.UserID,
		"workspaceId": fixture.WorkspaceID,
	})
}

// handleSignal is the POST /signals handler. Records the auth header,
// content-type, raw body, and parsed body, then responds 201.
func (f *fakeFlowAPI) handleSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+signalTokenFixture {
		f.t.Errorf("fake flow-api: signal Authorization header mismatch: got %q", auth)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var parsed capturedSignalBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		http.Error(w, "decode body: "+err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.signals = append(f.signals, capturedSignal{
		authorization: auth,
		contentType:   r.Header.Get("Content-Type"),
		rawBody:       append([]byte(nil), body...),
		parsed:        parsed,
	})
	f.mu.Unlock()
	f.signalHits.Add(1)

	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":"sig-fixture"}`))
}

// presenceFor fabricates the minimum *discordgo.PresenceUpdate the
// gateway handler needs: a User with an ID, a Status, and a GuildID.
// Activities are optional and omitted for the basic scenarios.
func presenceFor(snowflake, status string) *discordgo.PresenceUpdate {
	return &discordgo.PresenceUpdate{
		Presence: discordgo.Presence{
			User:   &discordgo.User{ID: snowflake},
			Status: discordgo.Status(status),
		},
		GuildID: "guild-fixture",
	}
}

// buildGateway constructs a wired Gateway pointed at the fake flow-api,
// with the supplied debounce window. The returned cleanup function
// drains the debouncer; tests register it via t.Cleanup.
func buildGateway(t *testing.T, fake *fakeFlowAPI, window time.Duration) (*gateway.Gateway, context.CancelFunc) {
	t.Helper()
	cfg := &config.Config{
		DiscordBotToken:    "fake-discord-bot-token",
		FlowAPIBaseURL:     fake.BaseURL(),
		FlowAPISignalToken: signalTokenFixture,
		DebounceSeconds:    int(window / time.Second),
		MetricsAddr:        ":0",
		LogLevel:           "debug",
		ShutdownTimeout:    time.Second,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	gw := gateway.New(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	emitter := gateway.NewEmitter(gateway.EmitterConfig{
		BaseURL:     cfg.FlowAPIBaseURL,
		SignalToken: cfg.FlowAPISignalToken,
		Logger:      logger,
	})
	gw.WireForTest(ctx, emitter, window)
	t.Cleanup(func() {
		gw.StopForTest()
		cancel()
	})
	return gw, cancel
}

// snapshotMetrics captures the counter values the suite asserts on.
// All counters are package-global; tests diff before/after to stay
// independent of execution order.
type metricsSnapshot struct {
	signalEmitted   float64
	signalFailed    float64
	dropNoUser      float64
	presenceUpdate  float64
	debounceDropped float64
}

func captureMetrics() metricsSnapshot {
	return metricsSnapshot{
		signalEmitted:   testutil.ToFloat64(obs.EventsTotal.WithLabelValues("signal_emitted")),
		signalFailed:    testutil.ToFloat64(obs.EventsTotal.WithLabelValues("signal_failed")),
		dropNoUser:      testutil.ToFloat64(obs.EventsTotal.WithLabelValues("drop_no_user")),
		presenceUpdate:  testutil.ToFloat64(obs.EventsTotal.WithLabelValues("presence_update")),
		debounceDropped: testutil.ToFloat64(obs.DebounceDroppedTotal),
	}
}

// diff returns the per-counter delta from before to after.
func (b metricsSnapshot) diff(after metricsSnapshot) metricsSnapshot {
	return metricsSnapshot{
		signalEmitted:   after.signalEmitted - b.signalEmitted,
		signalFailed:    after.signalFailed - b.signalFailed,
		dropNoUser:      after.dropNoUser - b.dropNoUser,
		presenceUpdate:  after.presenceUpdate - b.presenceUpdate,
		debounceDropped: after.debounceDropped - b.debounceDropped,
	}
}

// TestGateway_HappyPath asserts the single-event chain:
//
//	PresenceUpdate -> lookup 200 -> POST /signals 201
//
// Exactly one signal must be recorded, with the right
// source/kind/subjectType/subjectId/workspaceId fields and an
// Authorization header of "Bearer <token>".
func TestGateway_HappyPath(t *testing.T) {
	t.Parallel()

	fake := newFakeFlowAPI(t)
	const (
		snowflake   = "111111111111111111"
		userID      = "01913f1a-7777-7000-8000-000000000001"
		workspaceID = "01913f1a-7777-7000-8000-000000000002"
	)
	fake.SetLookup(snowflake, userID, workspaceID)

	gw, _ := buildGateway(t, fake, 50*time.Millisecond)
	before := captureMetrics()

	gw.DispatchForTest(presenceFor(snowflake, "online"))

	require.Eventually(t, func() bool { return fake.SignalCount() >= 1 },
		2*time.Second, 5*time.Millisecond,
		"expected exactly one POST /signals after the happy-path dispatch")

	// Give any spurious trailing emit a window to fire; it must not.
	time.Sleep(100 * time.Millisecond)

	signals := fake.Signals()
	require.Len(t, signals, 1, "expected a single POST /signals for one presence update")
	require.Equal(t, 1, fake.LookupCount(), "lookup must run exactly once")

	got := signals[0]
	require.Equal(t, "Bearer "+signalTokenFixture, got.authorization,
		"Authorization header must carry the signal bearer token")
	require.Equal(t, "application/json", got.contentType,
		"emit request must be application/json")
	require.Equal(t, workspaceID, got.parsed.WorkspaceID, "workspaceId must come from the resolver")
	require.Equal(t, "discord", got.parsed.Source, "source must be 'discord'")
	require.Equal(t, "discord.presence", got.parsed.Kind, "kind must be 'discord.presence'")
	require.Equal(t, "user", got.parsed.SubjectType, "subjectType must be 'user'")
	require.Equal(t, userID, got.parsed.SubjectID, "subjectId must be the resolved flow user id")

	// Payload must decode and carry the presence status.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.parsed.Payload, &payload))
	require.Equal(t, "online", payload["status"], "payload.status must mirror the gateway event")
	require.Equal(t, "guild-fixture", payload["guildId"], "payload.guildId must mirror the gateway event")

	diff := before.diff(captureMetrics())
	// Only positive-direction assertions: the package-global Prometheus
	// counters are shared across parallel tests, so "==0" deltas can be
	// poisoned by sibling tests incrementing the same metric. The
	// happy-path contract here is "at least one signal_emitted + at
	// least one presence_update came from THIS test".
	require.GreaterOrEqual(t, diff.signalEmitted, 1.0, "signal_emitted counter must increment by at least 1")
	require.GreaterOrEqual(t, diff.presenceUpdate, 1.0, "presence_update counter must increment for each dispatched event")
}

// TestGateway_DebounceBurst fires three PresenceUpdate events for the
// same user within the debounce window. The worker must emit at most
// two POST /signals (leading + trailing replay) and the
// debounce-dropped counter must increment by >=1.
func TestGateway_DebounceBurst(t *testing.T) {
	t.Parallel()

	fake := newFakeFlowAPI(t)
	const (
		snowflake   = "222222222222222222"
		userID      = "01913f1a-7777-7000-8000-000000000003"
		workspaceID = "01913f1a-7777-7000-8000-000000000004"
	)
	fake.SetLookup(snowflake, userID, workspaceID)

	// 80ms window is short enough to keep the test fast, long enough
	// that scheduling jitter doesn't escape the window between the
	// three back-to-back dispatches.
	const window = 80 * time.Millisecond
	gw, _ := buildGateway(t, fake, window)
	before := captureMetrics()

	// Three events in rapid succession. The first emits immediately
	// (leading-edge), the second and third should collapse into a
	// single trailing emit carrying the last payload.
	gw.DispatchForTest(presenceFor(snowflake, "online"))
	gw.DispatchForTest(presenceFor(snowflake, "idle"))
	gw.DispatchForTest(presenceFor(snowflake, "dnd"))

	// Wait long enough for the trailing timer to fire and the HTTP
	// roundtrip to complete. 6x the window absorbs scheduling jitter
	// on overloaded CI hosts.
	require.Eventually(t, func() bool { return fake.SignalCount() >= 2 },
		6*window, 5*time.Millisecond,
		"expected leading + trailing emit (2 total) within 6x the debounce window")

	// Settle: give any extra (incorrect) trailing emits time to fire
	// so we can pin the upper bound at <=2.
	time.Sleep(2 * window)
	signals := fake.Signals()
	require.LessOrEqual(t, len(signals), 2,
		"three bursty events must collapse to at most 2 emits (leading + trailing replay)")
	require.GreaterOrEqual(t, len(signals), 1,
		"at least the leading-edge emit must fire")

	// When both emits land, the trailing one must carry the LAST
	// payload (dnd), not an intermediate one. This is the contract
	// debounce_test.go enforces at the debouncer layer; re-asserting
	// here verifies the wiring between handler and emitter preserves
	// payload order.
	if len(signals) == 2 {
		var leadingPayload, trailingPayload map[string]any
		require.NoError(t, json.Unmarshal(signals[0].parsed.Payload, &leadingPayload))
		require.NoError(t, json.Unmarshal(signals[1].parsed.Payload, &trailingPayload))
		require.Equal(t, "online", leadingPayload["status"],
			"leading-edge emit must carry the first payload")
		require.Equal(t, "dnd", trailingPayload["status"],
			"trailing emit must carry the LAST payload from the burst")
	}

	diff := before.diff(captureMetrics())
	require.GreaterOrEqual(t, diff.debounceDropped, 1.0,
		"debounce_dropped counter must increment for the suppressed in-flight event(s)")
	require.GreaterOrEqual(t, diff.presenceUpdate, 3.0,
		"presence_update counter must increment once per dispatched event")
}

// TestGateway_DropUnknownUser asserts the "snowflake not bound to any
// flow user" path: lookup returns 404, the worker drops the event,
// drop_no_user counter increments, and ZERO POSTs to /signals occur.
func TestGateway_DropUnknownUser(t *testing.T) {
	t.Parallel()

	fake := newFakeFlowAPI(t)
	// Intentionally do NOT call SetLookup — the snowflake is unknown.
	const snowflake = "333333333333333333"

	gw, _ := buildGateway(t, fake, 50*time.Millisecond)
	before := captureMetrics()

	gw.DispatchForTest(presenceFor(snowflake, "online"))

	// The lookup must have run; wait for it before checking that
	// /signals stayed quiet.
	require.Eventually(t, func() bool { return fake.LookupCount() >= 1 },
		2*time.Second, 5*time.Millisecond,
		"lookup must run before the worker decides to drop")

	// Give the trailing-window plenty of slack to fire (it shouldn't).
	time.Sleep(150 * time.Millisecond)

	require.Equal(t, 0, fake.SignalCount(),
		"unknown-user presence must NOT result in any POST /signals")

	diff := before.diff(captureMetrics())
	// Positive-direction only: see HappyPath. The strict assertion
	// that the worker did NOT emit a signal is enforced via
	// fake.SignalCount() above, which IS test-local and reliable.
	require.GreaterOrEqual(t, diff.dropNoUser, 1.0,
		"drop_no_user counter must increment by 1 for the 404 lookup")
}

// fakeSession is the no-op SessionAdapterForTest implementation used by
// the reconnect probe. It records Open/Close call counts so the test
// can assert the session lifecycle was driven correctly.
type fakeSession struct {
	openCalls  atomic.Int32
	closeCalls atomic.Int32
}

func (f *fakeSession) Open() error  { f.openCalls.Add(1); return nil }
func (f *fakeSession) Close() error { f.closeCalls.Add(1); return nil }
func (f *fakeSession) AddHandler(_ interface{}) func() {
	// Return a no-op remove func; the gateway calls it during Stop.
	return func() {}
}

// TestGateway_ReconnectAfterDisconnect probes the gateway's
// post-shutdown reusability: Start -> Stop -> Start must succeed when
// a fresh session factory is supplied each time.
//
// Production reconnect logic lives inside discordgo itself: the
// library transparently re-identifies on transient disconnects without
// the worker observing the underlying WS lifecycle. Asserting on
// discordgo's reconnect behaviour would be a library test, not a
// worker test; instead this case verifies that the worker's session
// factory + handler-registration sequence is structurally re-runnable
// after a clean Stop, which is what an operator-driven restart relies
// on (and what discordgo's internal reconnect re-creates handlers
// against in production).
//
// NOTE: The worker's Gateway.Start sets g.started=true and never
// resets it, so a literal Start->Stop->Start cycle on the same
// instance is rejected by design ("Start called twice"). This probe
// therefore creates two Gateway instances back-to-back, which is the
// realistic restart shape (cmd/gateway re-execs the binary).
func TestGateway_ReconnectAfterDisconnect(t *testing.T) {
	t.Parallel()

	fake := newFakeFlowAPI(t)
	const (
		snowflake   = "444444444444444444"
		userID      = "01913f1a-7777-7000-8000-000000000005"
		workspaceID = "01913f1a-7777-7000-8000-000000000006"
	)
	fake.SetLookup(snowflake, userID, workspaceID)

	// First "session": wire + dispatch + stop.
	gw1, _ := buildGateway(t, fake, 50*time.Millisecond)
	gw1.DispatchForTest(presenceFor(snowflake, "online"))
	require.Eventually(t, func() bool { return fake.SignalCount() >= 1 },
		2*time.Second, 5*time.Millisecond,
		"first session must emit one signal")
	gw1.StopForTest()

	// Second "session": fresh Gateway, fresh wiring. Must still emit.
	gw2, _ := buildGateway(t, fake, 50*time.Millisecond)
	gw2.DispatchForTest(presenceFor(snowflake, "dnd"))
	require.Eventually(t, func() bool { return fake.SignalCount() >= 2 },
		2*time.Second, 5*time.Millisecond,
		"second session (post-reconnect) must emit a fresh signal")

	signals := fake.Signals()
	require.Len(t, signals, 2,
		"reconnect probe should yield exactly two signals across two sessions")

	var firstPayload, secondPayload map[string]any
	require.NoError(t, json.Unmarshal(signals[0].parsed.Payload, &firstPayload))
	require.NoError(t, json.Unmarshal(signals[1].parsed.Payload, &secondPayload))
	require.Equal(t, "online", firstPayload["status"], "first session payload")
	require.Equal(t, "dnd", secondPayload["status"], "second session payload survives the reconnect cycle")
}

// TestGateway_SessionFactoryInvokedOnStart is the unit-style companion
// to the reconnect probe: it asserts that Start() actually calls the
// configured session factory and registers handlers, so the
// reconnect-via-restart pattern above corresponds to real session
// lifecycle work in production. Without this, the reconnect probe
// might pass while Start() silently no-ops.
//
// This test does not use the test seam (WireForTest); it drives the
// real Start() with a fake session adapter so the production code
// path inside openSession() is exercised end-to-end.
func TestGateway_SessionFactoryInvokedOnStart(t *testing.T) {
	t.Parallel()

	fake := newFakeFlowAPI(t)
	cfg := &config.Config{
		DiscordBotToken:    "fake-discord-bot-token",
		FlowAPIBaseURL:     fake.BaseURL(),
		FlowAPISignalToken: signalTokenFixture,
		DebounceSeconds:    1,
		MetricsAddr:        ":0",
		LogLevel:           "debug",
		ShutdownTimeout:    time.Second,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	gw := gateway.New(cfg, logger)

	sess := &fakeSession{}
	var factoryCalls atomic.Int32
	gw.SessionFactoryForTest(func(token string) (gateway.SessionAdapterForTest, error) {
		factoryCalls.Add(1)
		require.Equal(t, "fake-discord-bot-token", token,
			"factory must receive the configured bot token")
		return sess, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- gw.Start(ctx) }()

	// Start should bring the gateway up and block on ctx. Give it a
	// moment to walk through openSession().
	require.Eventually(t, func() bool { return sess.openCalls.Load() == 1 },
		2*time.Second, 5*time.Millisecond,
		"Start must call session.Open exactly once")
	require.Equal(t, int32(1), factoryCalls.Load(),
		"Start must invoke the session factory exactly once")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "Start must return nil on context cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}

	require.NoError(t, gw.Stop(context.Background()),
		"Stop must succeed after Start returns")
	require.Equal(t, int32(1), sess.closeCalls.Load(),
		"Stop must call session.Close exactly once")
}

// TestGateway_MissingTokenStaysUpWithGaugeZero asserts the graceful
// degradation contract: an empty bot token must NOT crash the process.
// Start must park nf_presence_discord_gateway_up at 0, block on
// ctx.Done() (so /metrics stays scrapable for alerting), and never
// invoke the session factory. Returning nil on cancel keeps
// lifecycle.Run from treating the missing token as a fatal exit.
func TestGateway_MissingTokenStaysUpWithGaugeZero(t *testing.T) {
	// Not parallel: this test asserts an exact value on the global
	// GatewayUp gauge, which sibling tests also mutate. Serialising it
	// keeps the gauge reading uncontended.
	fake := newFakeFlowAPI(t)
	cfg := &config.Config{
		DiscordBotToken:    "", // missing on purpose
		FlowAPIBaseURL:     fake.BaseURL(),
		FlowAPISignalToken: signalTokenFixture,
		DebounceSeconds:    1,
		MetricsAddr:        ":0",
		LogLevel:           "debug",
		ShutdownTimeout:    time.Second,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	gw := gateway.New(cfg, logger)

	var factoryCalls atomic.Int32
	gw.SessionFactoryForTest(func(string) (gateway.SessionAdapterForTest, error) {
		factoryCalls.Add(1)
		return &fakeSession{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- gw.Start(ctx) }()

	// Start must NOT return while ctx is live — the process stays up.
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(obs.GatewayUp) == 0
	}, 2*time.Second, 5*time.Millisecond,
		"gateway_up must be parked at 0 while the token is missing")
	select {
	case err := <-done:
		t.Fatalf("Start returned early on missing token (err=%v); it must stay up and block on ctx", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.Equal(t, int32(0), factoryCalls.Load(),
		"missing token must short-circuit before opening a Discord session")

	// Cancelling ctx must unblock Start cleanly with no error so
	// lifecycle.Run shuts down gracefully rather than exit(1).
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "Start must return nil on context cancel even with a missing token")
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}
