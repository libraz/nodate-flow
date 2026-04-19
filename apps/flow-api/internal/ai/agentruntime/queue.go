package agentruntime

import (
	"context"
	"errors"
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
type InProcessQueue struct {
	mu     sync.Mutex
	seen   map[string]struct{}
	buffer chan Run
}

// NewInProcessQueue returns an InProcessQueue with the given buffer
// capacity. A full buffer causes Enqueue to block until the worker
// drains it, which matches the behavior operators want in a single-
// binary deployment (backpressure instead of data loss).
func NewInProcessQueue(capacity int) *InProcessQueue {
	if capacity <= 0 {
		capacity = 64
	}
	return &InProcessQueue{
		seen:   make(map[string]struct{}),
		buffer: make(chan Run, capacity),
	}
}

// Enqueue implements [Queue].
func (q *InProcessQueue) Enqueue(ctx context.Context, r Run) error {
	q.mu.Lock()
	if _, ok := q.seen[r.DedupeKey]; ok {
		q.mu.Unlock()
		return ErrDuplicate
	}
	q.seen[r.DedupeKey] = struct{}{}
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

// Claim implements [Queue].
func (q *InProcessQueue) Claim(ctx context.Context) (Run, error) {
	select {
	case r := <-q.buffer:
		return r, nil
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

// Ack implements [Queue]. InProcess acks drop the dedupe entry so
// the same agent can be rescheduled at the next tick.
func (q *InProcessQueue) Ack(_ context.Context, dedupeKey string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.seen, dedupeKey)
	return nil
}

// Nack implements [Queue]. The in-process queue has no retry budget;
// it simply drops the dedupe entry and re-enqueues with Attempts+1
// if the caller re-submits. Persistent queues should increment an
// attempts column and move past a max retry count.
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
}

// Loop claims runs until ctx is cancelled. It Ack/Nacks based on the
// Runner's return value so the queue state stays consistent even
// when the LLM call fails.
func (w *Worker) Loop(ctx context.Context) {
	if w.Now == nil {
		w.Now = time.Now
	}
	for {
		r, err := w.Queue.Claim(ctx)
		if err != nil {
			return
		}
		if runErr := w.Runner.Run(ctx, r.Job, w.Now()); runErr != nil {
			_ = w.Queue.Nack(ctx, r.DedupeKey, runErr)
			continue
		}
		_ = w.Queue.Ack(ctx, r.DedupeKey)
	}
}
