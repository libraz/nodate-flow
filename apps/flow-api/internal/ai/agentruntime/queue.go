package agentruntime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Run is a single queued agent execution: the scheduler produces
// Runs, the worker consumes them. Splitting Job (the thing that is
// due) from Run (the thing being executed) is what lets the two
// sides scale independently once they move into separate processes.
type Run struct {
	// DedupeKey prevents double-enqueue across scheduler replicas. A
	// typical shape is "<agentId>:<scheduledAt.Unix()>". Queues MUST
	// reject a second Enqueue with the same key.
	DedupeKey   string
	Job         Job
	ScheduledAt time.Time
	Attempts    int
}

// ErrDuplicate is returned by [Queue.Enqueue] when a Run with the
// same dedupe key already exists. Callers treat it as a no-op.
var ErrDuplicate = errors.New("agentruntime: duplicate run")

// Queue is the transport between scheduler and worker. The default
// in-process implementation ([InProcessQueue]) keeps the current
// single-binary deployment working; a MySQL-backed implementation
// using `SELECT ... FOR UPDATE SKIP LOCKED` unlocks multi-replica
// workers and lives in a sibling file once the schema lands.
type Queue interface {
	// Enqueue registers a new Run. Returns [ErrDuplicate] when the
	// dedupe key is already known.
	Enqueue(ctx context.Context, r Run) error
	// Claim blocks until a Run is available or ctx is cancelled.
	// Returns the claimed Run; the worker is responsible for calling
	// Ack / Nack when done.
	Claim(ctx context.Context) (Run, error)
	// Ack marks a Run as successfully completed.
	Ack(ctx context.Context, dedupeKey string) error
	// Nack returns a Run to the queue for retry (bounded by Attempts).
	Nack(ctx context.Context, dedupeKey string, err error) error
}

// InProcessQueue is an in-memory [Queue] used by the default single
// binary deployment. It is NOT safe across processes — for
// multi-replica k8s deployments, swap in a DB or Redis-backed queue.
//
// Dedupe matches the MySQL queue: a key stays known after its run is
// acked or nacked, as the agent_runs row does, until [DefaultRetention]
// has passed, as the purger deletes that row.
type InProcessQueue struct {
	mu sync.Mutex
	// seen holds every known key: zero while queued or running, the
	// completion time once acked or nacked.
	seen      map[string]time.Time
	lastPrune time.Time
	now       func() time.Time
	buffer    chan Run
}

// inProcessPruneEvery bounds how often Enqueue scans seen for keys past
// [DefaultRetention].
const inProcessPruneEvery = time.Hour

// NewInProcessQueue returns an InProcessQueue with the given buffer
// capacity. A full buffer causes Enqueue to block until the worker
// drains it, which matches the behavior operators want in a single-
// binary deployment (backpressure instead of data loss).
func NewInProcessQueue(capacity int) *InProcessQueue {
	if capacity <= 0 {
		capacity = 64
	}
	return &InProcessQueue{
		seen:   make(map[string]time.Time),
		now:    time.Now,
		buffer: make(chan Run, capacity),
	}
}

// Enqueue implements [Queue].
func (q *InProcessQueue) Enqueue(ctx context.Context, r Run) error {
	q.mu.Lock()
	q.prune()
	if _, ok := q.seen[r.DedupeKey]; ok {
		q.mu.Unlock()
		return ErrDuplicate
	}
	q.seen[r.DedupeKey] = time.Time{}
	q.mu.Unlock()
	select {
	case q.buffer <- r:
		return nil
	case <-ctx.Done():
		q.mu.Lock()
		delete(q.seen, r.DedupeKey)
		q.mu.Unlock()
		return ctx.Err()
	}
}

// prune forgets keys whose run finished more than [DefaultRetention] ago.
// The caller holds q.mu.
func (q *InProcessQueue) prune() {
	now := q.now()
	if now.Sub(q.lastPrune) < inProcessPruneEvery {
		return
	}
	q.lastPrune = now
	for key, finished := range q.seen {
		if !finished.IsZero() && now.Sub(finished) > DefaultRetention {
			delete(q.seen, key)
		}
	}
}

// Claim implements [Queue].
func (q *InProcessQueue) Claim(ctx context.Context) (Run, error) {
	select {
	case r := <-q.buffer:
		return r, nil
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

// Ack implements [Queue]. The key stays known, so a replay of the same
// run is still rejected as [ErrDuplicate].
func (q *InProcessQueue) Ack(_ context.Context, dedupeKey string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.seen[dedupeKey]; ok {
		q.seen[dedupeKey] = q.now()
	}
	return nil
}

// Nack implements [Queue]. Like the MySQL queue it parks the run as
// finished: there is no retry budget, and the key stays known.
func (q *InProcessQueue) Nack(ctx context.Context, dedupeKey string, _ error) error {
	return q.Ack(ctx, dedupeKey)
}

// Worker is the pull-side of the scheduler/worker split. It calls
// Queue.Claim in a loop and hands each Run to a [Runner]. Run Stop
// from another goroutine to drain and exit. Workers can scale
// independently of the scheduler — run N of them against the same
// queue to parallelize LLM-bound agent execution.
type Worker struct {
	Queue  Queue
	Runner Runner
	Now    func() time.Time
	// Name identifies this worker in logs. Defaults to "agent.worker".
	Name string
	// Logger receives claim failures. Defaults to slog.Default().
	Logger *slog.Logger
	// ClaimBackoff is the pause after a failed claim. Defaults to
	// [defaultClaimBackoff].
	ClaimBackoff time.Duration
}

// defaultClaimBackoff is how long a worker waits after a failed claim
// before trying again. Long enough that a database outage does not turn
// into a spin, short enough that agents resume promptly once it clears.
const defaultClaimBackoff = time.Second

// Loop claims runs until ctx is cancelled. It Ack/Nacks based on the
// Runner's return value so the queue state stays consistent even when
// the LLM call fails.
//
// A failed claim is retried, not fatal. The queue reads the database on
// every claim, so one deadlock, one lock-wait timeout or one dropped
// connection used to end the goroutine — and with every worker on the
// same database they tended to end together. The process stayed up and
// healthy to every probe with no agent ever running again, and the only
// trace was the absence of work. Each failure is logged with the
// consecutive count so a persistent outage is distinguishable from a
// blip.
func (w *Worker) Loop(ctx context.Context) {
	if w.Now == nil {
		w.Now = time.Now
	}
	log := w.Logger
	if log == nil {
		log = slog.Default()
	}
	name := w.Name
	if name == "" {
		name = "agent.worker"
	}
	backoff := w.ClaimBackoff
	if backoff <= 0 {
		backoff = defaultClaimBackoff
	}

	consecutiveFailures := 0
	for {
		r, err := w.Queue.Claim(ctx)
		if err != nil {
			// Cancellation is the one clean exit; anything else is a
			// database fault the worker has to outlive.
			if ctx.Err() != nil {
				log.InfoContext(ctx, "agent worker stopped", "worker", name)
				return
			}
			consecutiveFailures++
			log.ErrorContext(ctx, "agent worker: claim failed; retrying",
				"worker", name,
				"consecutive_failures", consecutiveFailures,
				"backoff", backoff,
				"err", err)
			select {
			case <-ctx.Done():
				log.InfoContext(ctx, "agent worker stopped", "worker", name)
				return
			case <-time.After(backoff):
			}
			continue
		}
		if consecutiveFailures > 0 {
			log.InfoContext(ctx, "agent worker: claims recovered",
				"worker", name,
				"after_failures", consecutiveFailures)
			consecutiveFailures = 0
		}
		if runErr := w.Runner.Run(ctx, r.Job, w.Now()); runErr != nil {
			_ = w.Queue.Nack(ctx, r.DedupeKey, runErr)
			continue
		}
		_ = w.Queue.Ack(ctx, r.DedupeKey)
	}
}
