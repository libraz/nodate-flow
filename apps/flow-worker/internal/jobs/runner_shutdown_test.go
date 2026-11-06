package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type shutdownBlockingJob struct {
	started    chan struct{}
	canceled   chan struct{}
	release    chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

func (j *shutdownBlockingJob) Name() string { return "shutdown-blocking" }

func (j *shutdownBlockingJob) Tick(ctx context.Context, _ time.Time) error {
	j.startOnce.Do(func() { close(j.started) })
	select {
	case <-ctx.Done():
		j.cancelOnce.Do(func() { close(j.canceled) })
		<-j.release
	case <-j.release:
	}
	return nil
}

func TestRunnerShutdownWaitsForWholeLoop(t *testing.T) {
	job := &shutdownBlockingJob{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	runner := &Runner{
		Interval: time.Millisecond,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	runner.Register(job)
	var releaseOnce sync.Once
	releaseJob := func() { releaseOnce.Do(func() { close(job.release) }) }
	t.Cleanup(func() {
		releaseJob()
		_ = runner.Shutdown(context.Background())
	})
	if err := runner.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-job.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start the job")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err := runner.Shutdown(shutdownCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline while job is blocked", err)
	}
	select {
	case <-job.canceled:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel the running job")
	}

	releaseJob()
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after cooperative job completion: %v", err)
	}
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
}

func TestRunnerShutdownBeforeStartIsSafe(t *testing.T) {
	var runner Runner
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
}
