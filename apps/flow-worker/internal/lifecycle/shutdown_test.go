package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFinishShutdownSharesOneDeadlineAndCancelsBeforeWaits(t *testing.T) {
	const budget = 80 * time.Millisecond
	var stopCalls atomic.Int32
	entered := make(chan time.Time, 2)
	wait := func(ctx context.Context) error {
		if got := stopCalls.Load(); got != 1 {
			return errors.New("a shutdown wait began before runner cancellation")
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("drain context has no deadline")
		}
		entered <- deadline
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- finishShutdown(ctx,
			func() { stopCalls.Add(1) },
			wait,
			wait,
			nil,
			func() error { return nil },
		)
	}()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("test context has no deadline")
	}
	for range 2 {
		select {
		case got := <-entered:
			if !got.Equal(deadline) {
				t.Fatalf("drain deadline = %v, want the one shared deadline %v", got, deadline)
			}
		case <-ctx.Done():
			t.Fatal("both shutdown drains were not running concurrently within the single budget")
		}
	}
	if got := stopCalls.Load(); got != 1 {
		t.Fatalf("runner cancellation called %d times before draining, want 1", got)
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("finishShutdown error = %v, want the shared deadline error", err)
	}
}

func TestFinishShutdownClosesDatabaseAfterCooperativeRunnerDrain(t *testing.T) {
	jobRelease := make(chan struct{})
	jobFinished := make(chan struct{})
	waitStarted := make(chan struct{})
	var dbSawFinished atomic.Bool

	err := finishShutdown(context.Background(),
		func() { close(jobRelease) },
		func(context.Context) error {
			close(waitStarted)
			<-jobRelease
			close(jobFinished)
			return nil
		},
		func(context.Context) error { return nil },
		nil,
		func() error {
			select {
			case <-jobFinished:
				dbSawFinished.Store(true)
			default:
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("finishShutdown: %v", err)
	}
	<-waitStarted
	if !dbSawFinished.Load() {
		t.Fatal("database close ran before the cooperative runner job finished")
	}
}
