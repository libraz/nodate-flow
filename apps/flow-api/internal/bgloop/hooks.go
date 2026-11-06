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
	ctx     context.Context
	cancel  context.CancelFunc

	wg       sync.WaitGroup
	mu       sync.Mutex
	sealed   bool
	waitOnce sync.Once
	drained  chan struct{}
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
	ctx, cancel := context.WithCancel(context.Background())
	return &HookRunner{
		name:    name,
		timeout: timeout,
		sem:     make(chan struct{}, concurrency),
		ctx:     ctx,
		cancel:  cancel,
		drained: make(chan struct{}),
	}
}

// Go accepts job unless [HookRunner.Shutdown] has begun, and reports
// whether it did. The job keeps ctx's values but not its cancellation,
// waits for a free slot, and then runs under timeout (the runner's
// default when zero). A panic is recovered and logged so one event's bad
// data cannot end the process.
func (r *HookRunner) Go(ctx context.Context, timeout time.Duration, job func(ctx context.Context)) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.sealed {
		r.mu.Unlock()
		return false
	}
	r.wg.Add(1)
	r.mu.Unlock()

	if timeout <= 0 {
		timeout = r.timeout
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		defer r.wg.Done()
		select {
		case r.sem <- struct{}{}:
		case <-r.ctx.Done():
			return
		}
		defer func() { <-r.sem }()
		// The semaphore and cancellation can become ready together. Check
		// again after acquiring a slot so a queued job never starts after
		// the runner has been force-cancelled.
		if r.ctx.Err() != nil {
			return
		}
		defer func() {
			if v := recover(); v != nil {
				slog.ErrorContext(detached, "event hook job panicked",
					"runner", r.name,
					"panic", formatPanic(v),
					"stack", string(debug.Stack()))
			}
		}()
		baseCtx, cancelBase := context.WithCancel(detached)
		stopRunnerCancel := context.AfterFunc(r.ctx, cancelBase)
		defer func() {
			stopRunnerCancel()
			cancelBase()
		}()
		runCtx, cancel := context.WithTimeout(baseCtx, timeout)
		defer cancel()
		job(runCtx)
	}()
	return true
}

// Seal stops accepting new jobs. Accepted jobs remain detached and keep
// running until they finish or the runner is force-cancelled.
func (r *HookRunner) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Abort seals the runner and cancels queued and running jobs. Queued jobs
// select cancellation while waiting for a semaphore slot and do not run
// if cancellation races with slot acquisition.
func (r *HookRunner) Abort() {
	r.Seal()
	r.cancel()
}

// Drain waits for every accepted job after sealing, or until ctx is done.
// A cancelled drain force-cancels queued and running jobs. Safe to call
// more than once.
func (r *HookRunner) Drain(ctx context.Context) error {
	r.Seal()
	r.waitOnce.Do(func() {
		go func() {
			r.wg.Wait()
			close(r.drained)
		}()
	})
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-r.drained:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.Abort()
		return ctx.Err()
	}
}

// Shutdown stops accepting jobs and waits for every accepted one, or
// until ctx is done. Safe to call more than once.
func (r *HookRunner) Shutdown(ctx context.Context) error {
	return r.Drain(ctx)
}
