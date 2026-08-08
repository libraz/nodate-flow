// Package agentruntime hosts the lightweight in-process scheduler that
// fires AI agents on cron / on-event / manual triggers.
//
// The scheduler is intentionally minimal: a single goroutine ticks
// once a minute, asks a [Source] for due agents, and hands each one to
// a [Runner]. Cron expression parsing is delegated so this package
// stays free of external dependencies and easy to unit test.
package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

// Job is the minimal description of an agent the scheduler needs to
// know about. The Source decides what counts as "due"; this struct is
// only data.
type Job struct {
	AgentID uint32
	WsID    uint32
	Paused  bool
	// SourceEventID is the events.id row whose append woke the agent,
	// or 0 for scheduler-driven (interval) and manual runs. The runner
	// uses it to look up the source event's task_id so the emitted
	// ai.agent.run.* events stay bound to the same task and surface in
	// the task's run-history view.
	SourceEventID uint64
	// DedupeKey is the agent_runs.dedupe_key value that the scheduler /
	// enqueuer assigned to this job. The runner uses it to recover the
	// dispatch hint (signal id for the signaljudge path, currently)
	// without re-querying the queue. Empty when the run was driven
	// in-process without a dedupe key.
	DedupeKey string
}

// Source returns the set of jobs that should run at the given tick.
// Implementations typically wrap a sqlc query that scans `ai_agents`
// for rows with a non-null `cron_expr` and `paused = FALSE`.
type Source interface {
	Due(ctx context.Context, now time.Time) ([]Job, error)
}

// Runner executes a single agent run. Implementations call into the
// LLM provider stack and write `ai.agent.run.*` events. The runtime
// itself never touches an LLM client directly so tests can swap in a
// counter-only stub.
type Runner interface {
	Run(ctx context.Context, job Job, now time.Time) error
}

// ErrAlreadyRunning is returned by [Scheduler.Start] when the
// scheduler has already been started.
var ErrAlreadyRunning = errors.New("agentruntime: scheduler already running")

// Scheduler ticks once per [Interval] and dispatches due jobs to the
// runner. It is safe for concurrent Stop calls; only the first
// observed cancellation actually unwinds.
type Scheduler struct {
	Source Source
	Runner Runner
	// Queue is optional. When set, tick enqueues Runs instead of
	// calling Runner directly — this is the scheduler/worker split
	// path used in multi-replica deployments. When nil, tick falls
	// back to synchronous Runner.Run so the single-binary default
	// keeps working without a DB-backed queue.
	Queue    Queue
	Interval time.Duration
	Now      func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool

	// Observability counters.
	ticks    uint64
	dispatch uint64
	errors   uint64
}

// Start begins the tick loop in a background goroutine. It returns
// immediately. Calling Start while already running returns
// [ErrAlreadyRunning].
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrAlreadyRunning
	}
	if s.Interval <= 0 {
		s.Interval = time.Minute
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	// Supervised so a panic in a tick does not take the process down
	// and a loop that returns is restarted rather than silently gone.
	go bgloop.Run(runCtx, "agent.scheduler", nil, s.loop)
	return nil
}

// Stop signals the loop to exit. It is safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
}

// Stats returns a snapshot of the scheduler counters.
func (s *Scheduler) Stats() (ticks, dispatched, errs uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticks, s.dispatch, s.errors
}

func (s *Scheduler) loop(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	// Tick once immediately so tests with a small Interval don't have
	// to wait a full period for the first dispatch.
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// dedupeKey names the interval slot now falls in, so every replica
// scheduling the same agent for the same tick produces the same key and
// the queue keeps exactly one run.
//
// The arithmetic is in [time.Duration] units throughout. Converting the
// interval to whole seconds first — which is what this did — truncates
// any sub-second interval to zero and divides by it, so an operator who
// wrote NF_FLOW_AGENT_TICK_INTERVAL=500ms got a panic on the first tick
// instead of a fast scheduler. Configuration now refuses sub-second
// intervals (see config.validateEnums), and this stays safe for any
// value a caller constructs the scheduler with directly.
func (s *Scheduler) dedupeKey(agentID uint32, now time.Time) string {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	return fmt.Sprintf("%d:%d", agentID, now.UnixNano()/int64(interval))
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.Now()
	s.mu.Lock()
	s.ticks++
	s.mu.Unlock()
	jobs, err := s.Source.Due(ctx, now)
	if err != nil {
		s.mu.Lock()
		s.errors++
		s.mu.Unlock()
		return
	}
	for _, j := range jobs {
		if j.Paused {
			continue
		}
		if s.Queue != nil {
			key := s.dedupeKey(j.AgentID, now)
			if err := s.Queue.Enqueue(ctx, Run{DedupeKey: key, Job: j, ScheduledAt: now}); err != nil {
				if errors.Is(err, ErrDuplicate) {
					continue
				}
				s.mu.Lock()
				s.errors++
				s.mu.Unlock()
				continue
			}
			s.mu.Lock()
			s.dispatch++
			s.mu.Unlock()
			continue
		}
		if err := s.Runner.Run(ctx, j, now); err != nil {
			s.mu.Lock()
			s.errors++
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.dispatch++
		s.mu.Unlock()
	}
}
