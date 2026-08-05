package agentruntime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyQueue fails Claim the first failures times, then serves one run
// and blocks until ctx is cancelled. It models the real failure: the
// MySQL-backed queue reads the database on every claim, so a deadlock,
// a lock-wait timeout or a dropped connection surfaces here.
type flakyQueue struct {
	mu       sync.Mutex
	failures int
	claims   int
	served   int
}

func (q *flakyQueue) Enqueue(context.Context, Run) error { return nil }

func (q *flakyQueue) Claim(ctx context.Context) (Run, error) {
	q.mu.Lock()
	q.claims++
	remaining := q.failures
	if remaining > 0 {
		q.failures--
	}
	q.mu.Unlock()

	if remaining > 0 {
		return Run{}, errors.New("Error 1213: Deadlock found when trying to get lock")
	}
	q.mu.Lock()
	first := q.served == 0
	q.served++
	q.mu.Unlock()
	if first {
		return Run{DedupeKey: "k1", Job: Job{WsID: 1, AgentID: 2}}, nil
	}
	<-ctx.Done()
	return Run{}, ctx.Err()
}

func (q *flakyQueue) Ack(context.Context, string) error         { return nil }
func (q *flakyQueue) Nack(context.Context, string, error) error { return nil }
func (q *flakyQueue) claimCount() int                           { q.mu.Lock(); defer q.mu.Unlock(); return q.claims }
func (q *flakyQueue) servedCount() int                          { q.mu.Lock(); defer q.mu.Unlock(); return q.served }

// countingRunner records the runs handed to it.
type countingRunner struct {
	mu  sync.Mutex
	run int
}

func (r *countingRunner) Run(context.Context, Job, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run++
	return nil
}

func (r *countingRunner) count() int { r.mu.Lock(); defer r.mu.Unlock(); return r.run }

// TestWorkerSurvivesTransientClaimFailures is the regression for agent
// workers going quiet. Claim talks to the database every time round, so
// a single transient error used to end the goroutine — and with every
// worker on the same database, they ended together. The process stayed
// up, /health passed, and no agent ever ran again; the only evidence was
// that nothing happened.
//
// The worker must keep claiming, run the job that arrives once the
// database recovers, and say something each time it failed.
func TestWorkerSurvivesTransientClaimFailures(t *testing.T) {
	q := &flakyQueue{failures: 3}
	runner := &countingRunner{}
	buf := &bytes.Buffer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &Worker{
		Queue:        q,
		Runner:       runner,
		Name:         "test.worker",
		Logger:       slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ClaimBackoff: time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Loop(ctx)
	}()

	waitUntil(t, func() bool { return runner.count() >= 1 },
		"the worker must keep claiming after transient failures and run the job that follows")

	cancel()
	<-done

	if got := q.claimCount(); got < 4 {
		t.Errorf("claims = %d, want at least 4 (3 failures + the successful one)", got)
	}

	logged := buf.String()
	if !strings.Contains(logged, "claim failed") {
		t.Errorf("every claim failure must be logged; got:\n%s", logged)
	}
	if !strings.Contains(logged, "consecutive_failures") {
		t.Errorf("the log must carry the consecutive count so a blip is distinguishable from an outage:\n%s", logged)
	}
	if !strings.Contains(logged, "claims recovered") {
		t.Errorf("recovery must be visible too, otherwise the last thing in the log is a failure:\n%s", logged)
	}
}

// TestWorkerStopsOnlyOnCancel pins the one clean exit. Without this the
// obvious fix to the above — never return — would leave the worker
// running past shutdown.
func TestWorkerStopsOnlyOnCancel(t *testing.T) {
	q := &flakyQueue{failures: 0}
	buf := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())

	w := &Worker{
		Queue:        q,
		Runner:       &countingRunner{},
		Name:         "test.worker.stop",
		Logger:       slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ClaimBackoff: time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Loop(ctx)
	}()

	waitUntil(t, func() bool { return q.servedCount() >= 1 }, "the worker must claim at least once")
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the worker must return once its context is cancelled")
	}
	if !strings.Contains(buf.String(), "agent worker stopped") {
		t.Errorf("a clean stop should be visible in the log:\n%s", buf.String())
	}
}

func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}
