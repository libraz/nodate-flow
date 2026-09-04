// Package bgloop supervises the long-running loops this binary starts
// in the background: the agent scheduler and workers, the agent_runs
// purger, the webhook delivery worker, the auto-action executor and the
// item-consistency reconciler.
//
// Two failure modes motivate it, and they look identical from outside —
// the feature simply stops happening, with nothing in the log:
//
//   - A panic in any goroutine takes the whole process down. One
//     workspace's data reaching a nil map in a background pass ends the
//     API for every tenant.
//   - A loop that returns on its own (a transient database error
//     mistaken for a fatal one) leaves the process alive and healthy to
//     every probe, minus the work that loop was doing.
//
// [Run] closes both: it recovers panics, restarts the loop, and records
// every occurrence. Recovering quietly would be no better than dying —
// worse, arguably, since the process keeps claiming to work — so each
// restart is logged at error level with the stack, counted per loop, and
// exported on /metrics.
package bgloop

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// The delay schedule between restarts. A loop that panics on every pass
// would otherwise spin: the delay grows to maxBackoff and stays there,
// keeping the failure visible in the log at a readable rate without ever
// giving up on the loop. Variables rather than constants so tests can
// shorten them; nothing in production reassigns them.
var (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// Stats is a point-in-time view of one supervised loop.
type Stats struct {
	// Panics is how many times the loop body panicked.
	Panics int
	// Returns is how many times it returned without panicking while its
	// context was still live — the silent-death case.
	Returns int
	// Restarts is how many times the supervisor started it again.
	Restarts int
	// LastFailure is the most recent panic value or return reason,
	// empty when the loop has never failed.
	LastFailure string
	// Running reports whether the supervisor is still supervising.
	Running bool
}

var (
	statsMu sync.Mutex
	stats   = map[string]*Stats{}
)

// Snapshot returns a copy of the recorded state for every supervised
// loop, keyed by name. Intended for health endpoints and tests.
func Snapshot() map[string]Stats {
	statsMu.Lock()
	defer statsMu.Unlock()
	out := make(map[string]Stats, len(stats))
	for name, s := range stats {
		out[name] = *s
	}
	return out
}

// ResetStats clears the recorded state. Tests use it to isolate cases.
func ResetStats() {
	statsMu.Lock()
	defer statsMu.Unlock()
	stats = map[string]*Stats{}
}

func record(name string, mutate func(*Stats)) {
	statsMu.Lock()
	defer statsMu.Unlock()
	s, ok := stats[name]
	if !ok {
		s = &Stats{}
		stats[name] = s
	}
	mutate(s)
}

// Run supervises fn under name until ctx is cancelled.
//
// fn is expected to block for the lifetime of the process. Whichever way
// it stops, the supervisor treats it as a fault worth reporting and
// starts it again after a backoff:
//
//   - a panic is recovered, logged with its stack, and counted;
//   - a plain return while ctx is still live is logged too, because a
//     loop that decided to stop on its own is the failure this package
//     exists to surface — the caller sees "started" in the log and
//     nothing afterwards.
//
// Cancelling ctx is the only clean way out; that path logs at info and
// returns. Run blocks, so callers start it with `go`.
func Run(ctx context.Context, name string, log *slog.Logger, fn func(ctx context.Context)) {
	if log == nil {
		log = slog.Default()
	}
	record(name, func(s *Stats) { s.Running = true })
	defer record(name, func(s *Stats) { s.Running = false })

	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			log.InfoContext(ctx, "background loop stopped", "loop", name)
			return
		}
		panicked := runOnce(ctx, name, log, fn)
		if ctx.Err() != nil {
			log.InfoContext(ctx, "background loop stopped", "loop", name)
			return
		}
		if !panicked {
			// The loop returned on its own with work still to do. That
			// is the quiet failure: nothing crashed, nothing logged,
			// the feature just stopped.
			record(name, func(s *Stats) {
				s.Returns++
				s.LastFailure = "returned while its context was still live"
			})
			restartsTotal.WithLabelValues(name, "returned").Inc()
			log.ErrorContext(ctx, "background loop returned early; restarting",
				"loop", name,
				"backoff", backoff)
		}
		record(name, func(s *Stats) { s.Restarts++ })
		select {
		case <-ctx.Done():
			log.InfoContext(ctx, "background loop stopped", "loop", name)
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// Start supervises fn under name in its own goroutine and hands back the
// single function that ends it.
//
// The loop runs under a context derived from ctx, and stop cancels that
// context. Cancelling is the only clean way to end a supervised loop: a
// loop ended through any other signal — a component's own Stop, a close
// on a quit channel — returns while its context is still live, which is
// exactly the fault [Run] restarts. It would come back up, in the middle
// of a shutdown, against a database pool being torn down around it.
//
// Handing back one stopper is why this exists. "Stop the loop" and
// "stop its supervisor" are otherwise two things every shutdown path has
// to keep together, and one of them drifting is invisible until a
// shutdown restarts what it just stopped.
//
// stop does not wait for fn to return: a loop that ignores its context
// would hold the shutdown open past its deadline, and the drain budget
// belongs to in-flight requests. Calling stop more than once is safe.
func Start(ctx context.Context, name string, log *slog.Logger, fn func(ctx context.Context)) (stop func()) {
	loopCtx, cancel := context.WithCancel(ctx)
	go Run(loopCtx, name, log, fn)
	return cancel
}

// runOnce calls fn and reports whether it panicked. The recover lives in
// its own frame so a panic unwinds only fn, leaving the supervisor loop
// intact.
func runOnce(ctx context.Context, name string, log *slog.Logger, fn func(ctx context.Context)) (panicked bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		panicked = true
		stack := string(debug.Stack())
		record(name, func(s *Stats) {
			s.Panics++
			s.LastFailure = formatPanic(r)
		})
		restartsTotal.WithLabelValues(name, "panic").Inc()
		log.ErrorContext(ctx, "background loop panicked; restarting",
			"loop", name,
			"panic", formatPanic(r),
			"stack", stack)
	}()
	fn(ctx)
	return false
}

// formatPanic renders a recovered value for logs without assuming it is
// an error or a string.
func formatPanic(r any) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
