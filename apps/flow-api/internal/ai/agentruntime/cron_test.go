package agentruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	jobs []Job
	err  error
	hits atomic.Uint64
}

func (f *fakeSource) Due(_ context.Context, _ time.Time) ([]Job, error) {
	f.hits.Add(1)
	return f.jobs, f.err
}

type fakeRunner struct {
	count atomic.Uint64
	fail  bool
}

func (f *fakeRunner) Run(_ context.Context, _ Job, _ time.Time) error {
	f.count.Add(1)
	if f.fail {
		return errors.New("boom")
	}
	return nil
}

func TestSchedulerDispatchesDueJobs(t *testing.T) {
	src := &fakeSource{jobs: []Job{{AgentID: 1}, {AgentID: 2}}}
	run := &fakeRunner{}
	s := &Scheduler{Source: src, Runner: run, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	s.Stop()

	if run.count.Load() < 2 {
		t.Fatalf("expected at least 2 runs, got %d", run.count.Load())
	}
}

func TestSchedulerSkipsPaused(t *testing.T) {
	src := &fakeSource{jobs: []Job{{AgentID: 1, Paused: true}}}
	run := &fakeRunner{}
	s := &Scheduler{Source: src, Runner: run, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	if run.count.Load() != 0 {
		t.Fatalf("paused job should not run, got %d", run.count.Load())
	}
}

func TestSchedulerCountsErrors(t *testing.T) {
	src := &fakeSource{jobs: []Job{{AgentID: 1}}}
	run := &fakeRunner{fail: true}
	s := &Scheduler{Source: src, Runner: run, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	_, dispatched, errs := s.Stats()
	if dispatched != 0 || errs == 0 {
		t.Fatalf("expected errs>0 dispatched=0, got d=%d e=%d", dispatched, errs)
	}
}

func TestSchedulerStartTwice(t *testing.T) {
	s := &Scheduler{Source: &fakeSource{}, Runner: &fakeRunner{}, Interval: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)
	defer s.Stop()
	if err := s.Start(ctx); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}
}
