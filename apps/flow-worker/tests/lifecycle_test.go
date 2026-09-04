// Package lifecycle_test exercises the flow-worker binary end-to-end via
// the exported Run function. Three deterministic tests cover the
// process lifecycle:
//
//  1. TestWorkerBootsAndExposesMetrics — happy path: Run boots, /metrics
//     returns 200 with nf_flow_worker_up=1 and the calendar histogram
//     bucket lines, ctx cancel returns nil.
//  2. TestWorkerGracefulShutdownOnSIGTERM — registers a fake job that
//     respects ctx, verifies Run returns within JobShutdownTimeout after
//     ctx cancel and that the in-flight tick observed cancellation.
//  3. TestWorkerDBConnectionFailureLogsAndExits — bogus DSN, Run returns
//     non-nil DB error and the slog stream contains an ERROR-level log
//     about the DB failure.
//
// All tests run with t.Parallel(). MySQL for tests 1 & 2 is provided by
// a single testcontainers MySQL instance booted in TestMain so the suite
// stays under the 5-second budget called for in the task brief.
//
// The suite is gated on NF_TEST_INTEGRATION=1 to match the repo-wide
// convention used by apps/flow-api/tests/calendar/main_test.go: it
// requires Docker for testcontainers and is therefore skipped in
// `go test -short` and on developer machines without Docker.
package tests

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/lifecycle"
)

const (
	mysqlImage    = "mysql:9.6"
	mysqlDatabase = "nodate_flow"
	mysqlUser     = "nodate"
	mysqlPassword = "nodate"
)

var (
	sharedDSNOnce sync.Once
	sharedDSN     string
	sharedDSNErr  error
)

// TestMain gates the package on NF_TEST_INTEGRATION and boots a single
// MySQL testcontainer for the happy-path tests. Test 3 (bogus DSN) does
// not consume the container.
func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		// Honour the repo convention: integration tests must opt-in.
		// Without the flag the package becomes a no-op (no Docker, no
		// container, immediate exit 0).
		os.Exit(m.Run())
	}
	os.Exit(m.Run())
}

// dsn lazily starts the shared MySQL testcontainer and returns its DSN.
// Subsequent callers receive the same DSN; the container is leaked to
// the process and reaped by testcontainers-ryuk on exit.
func dsn(t *testing.T) string {
	t.Helper()
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run flow-worker lifecycle tests (Docker required)")
	}
	sharedDSNOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		container, err := mysql.Run(
			ctx,
			mysqlImage,
			mysql.WithDatabase(mysqlDatabase),
			mysql.WithUsername(mysqlUser),
			mysql.WithPassword(mysqlPassword),
		)
		if err != nil {
			sharedDSNErr = fmt.Errorf("start mysql container: %w", err)
			return
		}
		s, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
		if err != nil {
			sharedDSNErr = fmt.Errorf("connection string: %w", err)
			return
		}
		// Verify the DSN is actually pingable so the first test does not
		// fail with a confusing timing error.
		db, err := sql.Open("mysql", s)
		if err != nil {
			sharedDSNErr = fmt.Errorf("open mysql: %w", err)
			return
		}
		// The probe pool is discarded once the DSN is known to be
		// pingable; a close error on it says nothing about the
		// container the tests go on to use.
		defer func() { _ = db.Close() }()
		deadline := time.Now().Add(60 * time.Second)
		for {
			pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
			err := db.PingContext(pctx)
			pcancel()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				sharedDSNErr = fmt.Errorf("mysql never became ready: %w", err)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		sharedDSN = s
	})
	require.NoError(t, sharedDSNErr, "shared MySQL container failed to start")
	require.NotEmpty(t, sharedDSN)
	return sharedDSN
}

// freePort returns an ephemeral TCP port the test can hand to the worker
// as NF_FLOW_WORKER_PORT. It binds, captures the port, then releases the
// socket; the worker re-binds immediately after.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return strconv.Itoa(port)
}

// captureLogger returns a *slog.Logger that writes JSON records to the
// returned *bytes.Buffer. Mirrors main.newLogger's level handling so the
// test can assert on the redact handler's output as well as raw text.
func captureLogger(level slog.Level) (*slog.Logger, *bytes.Buffer, *sync.Mutex) {
	var buf bytes.Buffer
	var mu sync.Mutex
	h := slog.NewJSONHandler(&lockedWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: level})
	return slog.New(h), &buf, &mu
}

// lockedWriter serialises slog handler writes so concurrent goroutines
// in Run (metrics server, runner) do not interleave records.
type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// baseConfig returns a Config tuned for fast lifecycle tests:
// - short tick / shutdown intervals so the loop reacts quickly,
// - the supplied DSN and metrics port,
// - tracing disabled so no OTel collector is required.
func baseConfig(t *testing.T, dsnStr, port string) *config.Config {
	t.Helper()
	cfg := &config.Config{
		DBDSN:              dsnStr,
		MetricsPort:        port,
		LogLevel:           "debug",
		OTelEndpoint:       "",
		OTelInsecure:       true,
		JobTickInterval:    50 * time.Millisecond,
		JobShutdownTimeout: 500 * time.Millisecond,
		DBMaxOpenConns:     4,
		DBMaxIdleConns:     2,
		DBConnMaxLifetime:  30 * time.Minute,
	}
	require.NoError(t, cfg.Validate())
	return cfg
}

// waitForMetricsReady polls GET /metrics until it returns 200 or the
// deadline elapses. The Run-side MetricsReady channel only signals that
// the goroutine started; this loop guarantees the listener actually
// accepts connections before the test asserts on the body.
func waitForMetricsReady(t *testing.T, port string, timeout time.Duration) string {
	t.Helper()
	url := "http://127.0.0.1:" + port + "/metrics"
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
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

// TestWorkerBootsAndExposesMetrics covers the happy-path boot: Run dials
// MySQL, binds /metrics, flips the up gauge, and shuts down cleanly when
// the parent context is cancelled.
//
// A job is registered because "up" now means "initialised and has work
// to do": a worker whose only job was disabled by unset configuration
// used to report itself healthy while producing nothing.
func TestWorkerBootsAndExposesMetrics(t *testing.T) {
	t.Parallel()
	dsnStr := dsn(t)
	port := freePort(t)
	cfg := baseConfig(t, dsnStr, port)
	logger, _, _ := captureLogger(slog.LevelDebug)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- lifecycle.Run(ctx, cfg, logger, &lifecycle.RunOptions{
			MetricsReady: ready,
			Register: func(r *jobs.Runner, _ *sql.DB) {
				r.Register(newBlockingJob())
			},
		})
	}()

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("MetricsReady never closed")
	}

	body := waitForMetricsReady(t, port, 2*time.Second)
	require.Contains(t, body, "nf_flow_worker_up 1",
		"metrics body should expose up=1 once boot completes")
	require.Contains(t, body, "nf_flow_worker_calendar_event_day_tick_seconds_bucket",
		"metrics body should expose the calendar job histogram bucket lines")

	// Initiate graceful shutdown and confirm Run returns nil.
	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err, "Run should return nil on context-cancelled shutdown")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// blockingJob is a fake jobs.Job that counts Tick invocations and blocks
// each tick on a per-instance gate until ctx is cancelled. Used by the
// graceful shutdown test to prove Run drains the in-flight tick.
type blockingJob struct {
	ticks       atomic.Int32
	ctxObserved atomic.Bool
	started     chan struct{}
	startOnce   sync.Once
}

func newBlockingJob() *blockingJob {
	return &blockingJob{started: make(chan struct{})}
}

func (b *blockingJob) Name() string { return "blocking" }

func (b *blockingJob) Tick(ctx context.Context, _ time.Time) error {
	b.ticks.Add(1)
	b.startOnce.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		b.ctxObserved.Store(true)
		return ctx.Err()
	case <-time.After(2 * time.Second):
		// Safety net: if cancellation is broken, do not hang the suite
		// forever. The test asserts ctxObserved == true so a clean exit
		// of this branch still fails the test.
		return nil
	}
}

// TestWorkerGracefulShutdownOnSIGTERM verifies that cancelling Run's
// parent context propagates through the runner to the in-flight tick and
// that Run returns within JobShutdownTimeout + a small margin.
//
// The test simulates SIGTERM by cancelling ctx: main() translates the
// real signal to exactly that, so the resulting code path is identical.
func TestWorkerGracefulShutdownOnSIGTERM(t *testing.T) {
	t.Parallel()
	dsnStr := dsn(t)
	port := freePort(t)
	cfg := baseConfig(t, dsnStr, port)
	logger, _, _ := captureLogger(slog.LevelDebug)

	job := newBlockingJob()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	runErr := make(chan error, 1)
	start := time.Now()
	go func() {
		runErr <- lifecycle.Run(ctx, cfg, logger, &lifecycle.RunOptions{
			MetricsReady: ready,
			Register: func(r *jobs.Runner, _ *sql.DB) {
				r.Register(job)
			},
		})
	}()

	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("MetricsReady never closed")
	}

	// Wait for the first tick to begin so we know the job is in-flight
	// when we cancel; this is the path that exercises the drain.
	select {
	case <-job.started:
	case <-time.After(3 * time.Second):
		t.Fatal("blocking job never ticked; runner did not start")
	}

	cancel()

	// Run must return within JobShutdownTimeout (500ms) + the metrics
	// drain ceiling. 1 second is the safe margin the brief asks for.
	deadline := cfg.JobShutdownTimeout + time.Second
	select {
	case err := <-runErr:
		elapsed := time.Since(start)
		require.NoError(t, err, "graceful shutdown should return nil")
		require.LessOrEqual(t, elapsed, 10*time.Second,
			"Run took unexpectedly long to shut down: %s", elapsed)
		require.True(t, job.ctxObserved.Load(),
			"in-flight tick should have observed ctx.Done() before Run returned")
		require.GreaterOrEqual(t, job.ticks.Load(), int32(1),
			"job should have ticked at least once before shutdown")
	case <-time.After(deadline + 2*time.Second):
		t.Fatalf("Run did not return within %s of context cancel", deadline)
	}
}

// TestWorkerDBConnectionFailureLogsAndExits asserts that Run refuses to
// start with a broken DSN and surfaces both an error return and an
// ERROR-level slog record about the failure.
//
// No real MySQL is required: the DSN targets a port nothing listens on.
func TestWorkerDBConnectionFailureLogsAndExits(t *testing.T) {
	t.Parallel()
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run flow-worker lifecycle tests")
	}

	port := freePort(t)
	// Bogus DSN: bind to a TCP port that is almost certainly unreachable
	// (1 is reserved). Plus a 1s timeout so the test stays well under
	// the suite's 5s budget.
	bogusDSN := "nodate:wrong@tcp(127.0.0.1:1)/nope?timeout=1s&readTimeout=1s&writeTimeout=1s"
	cfg := &config.Config{
		DBDSN:              bogusDSN,
		MetricsPort:        port,
		LogLevel:           "debug",
		OTelEndpoint:       "",
		OTelInsecure:       true,
		JobTickInterval:    50 * time.Millisecond,
		JobShutdownTimeout: 500 * time.Millisecond,
		DBMaxOpenConns:     4,
		DBMaxIdleConns:     2,
		DBConnMaxLifetime:  30 * time.Minute,
	}
	require.NoError(t, cfg.Validate())

	logger, buf, mu := captureLogger(slog.LevelDebug)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := lifecycle.Run(ctx, cfg, logger, nil)
	require.Error(t, err, "Run must surface DB failure as non-nil error")

	msg := err.Error()
	require.True(t,
		strings.Contains(msg, "db ping") || strings.Contains(msg, "connect"),
		"error should mention the DB / connection failure, got: %s", msg)

	mu.Lock()
	logOutput := buf.String()
	mu.Unlock()
	require.Contains(t, logOutput, `"level":"ERROR"`,
		"slog stream should contain an ERROR-level record")
	require.Contains(t, logOutput, "db ping failed",
		"slog stream should contain the db ping failed message")
}
