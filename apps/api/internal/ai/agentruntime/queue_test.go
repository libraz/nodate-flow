package agentruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestInProcessQueueDedupe(t *testing.T) {
	q := NewInProcessQueue(4)
	ctx := context.Background()
	r := Run{DedupeKey: "k1", Job: Job{AgentID: 1}}
	if err := q.Enqueue(ctx, r); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := q.Enqueue(ctx, r); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
	got, err := q.Claim(ctx)
	if err != nil || got.DedupeKey != "k1" {
		t.Fatalf("claim: got=%+v err=%v", got, err)
	}
	if err := q.Ack(ctx, "k1"); err != nil {
		t.Fatalf("ack: %v", err)
	}
	// After ack the same key can be re-enqueued at the next tick.
	if err := q.Enqueue(ctx, r); err != nil {
		t.Fatalf("re-enqueue after ack: %v", err)
	}
}

func TestWorkerLoopAcksOnSuccess(t *testing.T) {
	q := NewInProcessQueue(4)
	var ran atomic.Uint64
	runner := runnerFunc(func(_ context.Context, _ Job, _ time.Time) error {
		ran.Add(1)
		return nil
	})
	w := &Worker{Queue: q, Runner: runner}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Loop(ctx); close(done) }()

	_ = q.Enqueue(ctx, Run{DedupeKey: "a", Job: Job{AgentID: 1}})
	_ = q.Enqueue(ctx, Run{DedupeKey: "b", Job: Job{AgentID: 2}})

	// Busy wait for the worker to drain.
	deadline := time.Now().Add(time.Second)
	for ran.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if got := ran.Load(); got != 2 {
		t.Fatalf("runner ran %d times, want 2", got)
	}
}

type runnerFunc func(ctx context.Context, j Job, now time.Time) error

func (f runnerFunc) Run(ctx context.Context, j Job, now time.Time) error { return f(ctx, j, now) }
