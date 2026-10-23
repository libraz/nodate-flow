package bgloop

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// HookRunner runs the work an event hook hands off the append path.
//
// An eventbus notify hook is called synchronously from the append, so it
// must not block; the work it starts — notification rows, webhook
// delivery rows, relation suggestions — holds a database connection, and
// a goroutine per event with no bound let a burst of events take the
// whole pool, with the symptom appearing in unrelated request handlers.
// Every hook therefore submits here instead of starting its own
// goroutine, and gets the same four properties: detached from the
// request's cancellation, at most a fixed number of jobs running at once,
// each running job under a deadline, and a drain at shutdown that waits
// for every accepted job, queued ones included.
type HookRunner struct {
	name    string
	timeout time.Duration
	sem     chan struct{}

	wg       sync.WaitGroup
	mu       sync.RWMutex
	stopping bool
}

// defaultHookTimeout is the per-job deadline of a runner built without
// one.
const defaultHookTimeout = 30 * time.Second

// NewHookRunner returns a runner that runs at most concurrency jobs at
// once, each under timeout unless the submission names its own.
func NewHookRunner(name string, concurrency int, timeout time.Duration) *HookRunner {
	if concurrency <= 0 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	return &HookRunner{name: name, timeout: timeout, sem: make(chan struct{}, concurrency)}
}

// Go accepts job unless [HookRunner.Shutdown] has begun, and reports
// whether it did. The job keeps ctx's values but not its cancellation,
// waits for a free slot, and then runs under timeout (the runner's
// default when zero). A panic is recovered and logged so one event's bad
// data cannot end the process.
func (r *HookRunner) Go(ctx context.Context, timeout time.Duration, job func(ctx context.Context)) bool {
	r.mu.RLock()
	if r.stopping {
		r.mu.RUnlock()
		return false
	}
	r.wg.Add(1)
	r.mu.RUnlock()

	if timeout <= 0 {
		timeout = r.timeout
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		defer r.wg.Done()
		r.sem <- struct{}{}
		defer func() { <-r.sem }()
		defer func() {
			if v := recover(); v != nil {
				slog.ErrorContext(detached, "event hook job panicked",
					"runner", r.name,
					"panic", formatPanic(v),
					"stack", string(debug.Stack()))
			}
		}()
		runCtx, cancel := context.WithTimeout(detached, timeout)
		defer cancel()
		job(runCtx)
	}()
	return true
}

// Shutdown stops accepting jobs and waits for every accepted one, or
// until ctx is done. Safe to call more than once.
func (r *HookRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
