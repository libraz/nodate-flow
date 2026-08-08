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

// A sub-second interval used to reach `int64(Interval.Seconds())`,
// which truncates to zero, so the first queued tick divided by zero and
// took the loop down. Configuration refuses sub-second intervals now
// (config.validateEnums), but a scheduler constructed directly still
// has to survive one — the panic was in the key arithmetic, not in the
// value.
func TestSchedulerDedupeKeyHandlesSubSecondInterval(t *testing.T) {
	t.Parallel()
	s := &Scheduler{Interval: 500 * time.Millisecond}
	now := time.Unix(1_700_000_000, 0)

	first := s.dedupeKey(7, now)
	if first == "" {
		t.Fatal("dedupe key is empty")
	}
	// Two ticks one interval apart must land in different slots, or the
	// queue would collapse them into a single run.
	if second := s.dedupeKey(7, now.Add(500*time.Millisecond)); second == first {
		t.Fatalf("consecutive ticks share a dedupe slot: %q", first)
	}
	// Two agents in the same slot must not share a key.
	if other := s.dedupeKey(8, now); other == first {
		t.Fatalf("two agents share a dedupe key: %q", first)
	}
}

// Within one interval every tick must produce the same key, which is
// what makes two replicas scheduling the same agent enqueue one run.
func TestSchedulerDedupeKeyIsStableWithinAnInterval(t *testing.T) {
	t.Parallel()
	s := &Scheduler{Interval: time.Minute}
	base := time.Unix(1_700_000_000, 0).Truncate(time.Minute)

	want := s.dedupeKey(3, base)
	if got := s.dedupeKey(3, base.Add(59*time.Second)); got != want {
		t.Fatalf("key drifted inside one interval: %q vs %q", got, want)
	}
	if got := s.dedupeKey(3, base.Add(time.Minute)); got == want {
		t.Fatalf("key survived into the next interval: %q", got)
	}
}

// The whole tick path, not just the key helper: a scheduler with a
// queue and a sub-second interval must enqueue rather than panic.
func TestSchedulerTickWithSubSecondIntervalEnqueues(t *testing.T) {
	t.Parallel()
	q := NewInProcessQueue(4)
	s := &Scheduler{
		Source:   &fakeSource{jobs: []Job{{AgentID: 1}}},
		Runner:   &fakeRunner{},
		Queue:    q,
		Interval: 500 * time.Millisecond,
		Now:      time.Now,
	}
	s.tick(context.Background())

	_, dispatched, errs := s.Stats()
	if dispatched != 1 || errs != 0 {
		t.Fatalf("dispatched=%d errors=%d, want 1 and 0", dispatched, errs)
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
