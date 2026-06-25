// Package tests exercises the presence-discord binary end-to-end via
// the exported lifecycle.Run function. P8-1 ships only a smoke test:
// the binary boots, /metrics returns 200 with the three Phase 8
// metrics, and shutdown completes cleanly when ctx is cancelled. P8-5
// will land the integration tests that exercise the actual gateway
// behaviour (debounce, signal emission, unknown-user drop, reconnect).
//
// The fake gatewayRunner avoids any Discord WS dependency: P8-1's real
// gateway intentionally returns "not yet implemented" until P8-2
// finishes the wiring, so the smoke test cannot use it.
//
// This package does NOT require Docker. Unlike the flow-worker
// lifecycle tests, presence-discord has no MySQL dependency at the
// process level, so the smoke test runs unconditionally in CI under
// `go test ./...`.
package tests

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/presence-discord/internal/lifecycle"
)

// fakeGateway is a gatewayRunner that blocks Start until ctx is
// cancelled and records that Stop was invoked. It mirrors the contract
// lifecycle.Run depends on without touching Discord or the network.
type fakeGateway struct {
	startCalls atomic.Int32
	stopCalls  atomic.Int32
}

func (f *fakeGateway) Start(ctx context.Context) error {
	f.startCalls.Add(1)
	<-ctx.Done()
	return nil
}

func (f *fakeGateway) Stop(_ context.Context) error {
	f.stopCalls.Add(1)
	return nil
}

// freePort returns an ephemeral TCP port the test can hand to the
// gateway as NF_PRESENCE_METRICS_ADDR. It binds, captures the port,
// then releases the socket; the gateway re-binds immediately after.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// waitForMetricsReady polls GET /metrics until it returns 200 or the
// deadline elapses. The Run-side MetricsReady channel only signals that
// the goroutine started; this loop guarantees the listener actually
// accepts connections before the test asserts on the body.
func waitForMetricsReady(t *testing.T, addr string, timeout time.Duration) string {
	t.Helper()
	url := "http://" + addr + "/metrics"
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec // test-only loopback URL.
		if err == nil && resp.StatusCode == http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.NoError(t, readErr)
			return string(body)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("metrics endpoint never became ready: %v", lastErr)
	return ""
}

// TestGatewayBootsAndExposesMetrics is the P8-1 smoke test. It asserts
// that lifecycle.Run:
//
//  1. binds the metrics server,
//  2. starts the (fake) gateway,
//  3. exposes the three Phase 8 metrics,
//  4. returns nil within the shutdown timeout when ctx is cancelled,
//  5. invokes Stop on the gateway exactly once.
//
// The real Discord gateway implementation is exercised by P8-5's
// integration suite.
func TestGatewayBootsAndExposesMetrics(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	cfg := &config.Config{
		DiscordBotToken:    "fake-token-not-used",
		FlowAPIBaseURL:     "http://flow-api:8080",
		FlowAPISignalToken: "",
		DebounceSeconds:    5,
		MetricsAddr:        addr,
		LogLevel:           "debug",
		OTelEndpoint:       "",
		OTelInsecure:       true,
		ShutdownTimeout:    500 * time.Millisecond,
	}
	require.NoError(t, cfg.Validate())

	fake := &fakeGateway{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- lifecycle.Run(ctx, cfg, logger, &lifecycle.RunOptions{
			Gateway:      fake,
			MetricsReady: ready,
		})
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("MetricsReady never closed")
	}

	body := waitForMetricsReady(t, addr, 2*time.Second)
	require.Contains(t, body, "nf_presence_discord_gateway_up",
		"metrics body should expose the gateway up gauge")
	require.Contains(t, body, "nf_presence_discord_events_total",
		"metrics body should expose the events counter")
	require.Contains(t, body, "nf_presence_discord_debounce_dropped_total",
		"metrics body should expose the debounce drop counter")

	// Confirm Start was invoked exactly once before we cancel.
	require.Equal(t, int32(1), fake.startCalls.Load(),
		"Start should have been called exactly once")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err, "Run should return nil on context-cancelled shutdown")
	case <-time.After(cfg.ShutdownTimeout + 2*time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	require.Equal(t, int32(1), fake.stopCalls.Load(),
		"Stop should have been called exactly once during graceful shutdown")
}

// TestMissingTokenKeepsProcessUpWithGaugeZero is the end-to-end
// counterpart to the gateway-level missing-token test: it drives the
// REAL gateway through lifecycle.Run (no fake gatewayRunner) with an
// empty bot token and asserts the process stays up and scrapable rather
// than crash-looping. The metrics body must expose
// nf_presence_discord_gateway_up with value 0, and Run must return nil
// on context cancel.
func TestMissingTokenKeepsProcessUpWithGaugeZero(t *testing.T) {
	// Not parallel: asserts an exact value on the global GatewayUp gauge.
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	cfg := &config.Config{
		DiscordBotToken:    "", // missing on purpose
		FlowAPIBaseURL:     "http://flow-api:8080",
		FlowAPISignalToken: "also-empty-but-token-check-comes-first",
		DebounceSeconds:    5,
		MetricsAddr:        addr,
		LogLevel:           "debug",
		ShutdownTimeout:    500 * time.Millisecond,
	}
	require.NoError(t, cfg.Validate())

	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		// No opts.Gateway: lifecycle.Run constructs the real gateway,
		// which must degrade gracefully on the missing token.
		runErr <- lifecycle.Run(ctx, cfg, logger, &lifecycle.RunOptions{MetricsReady: ready})
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("MetricsReady never closed")
	}

	// The metrics endpoint must remain scrapable and report the gauge at
	// 0 so alerting can fire on the degraded gateway.
	require.Eventually(t, func() bool {
		body := waitForMetricsReady(t, addr, 2*time.Second)
		return strings.Contains(body, "nf_presence_discord_gateway_up 0")
	}, 3*time.Second, 50*time.Millisecond,
		"metrics must export nf_presence_discord_gateway_up 0 while the token is missing")

	// Run must NOT have exited on its own — the process is still up.
	select {
	case err := <-runErr:
		t.Fatalf("Run exited before cancel on missing token (err=%v); it must stay up", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err, "Run must return nil on context-cancelled shutdown even with a missing token")
	case <-time.After(cfg.ShutdownTimeout + 2*time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
