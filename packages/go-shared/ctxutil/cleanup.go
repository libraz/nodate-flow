// Package ctxutil provides small helpers for context lifetime
// management that recur across the api services. The package is
// intentionally tiny; add helpers only when the same pattern would
// otherwise be inlined in two or more handlers.
package ctxutil

import (
	"context"
	"time"
)

// Cleanup returns a context derived from parent for use by best-effort
// "compensating" work that must run even if the request that triggered
// it has already completed or been cancelled. The returned context:
//
//   - inherits values from parent (so trace ids, slog attrs, and
//     authn metadata flow into the cleanup);
//   - does NOT inherit cancellation (built on top of
//     [context.WithoutCancel]); and
//   - has a hard deadline of d so the cleanup cannot leak goroutines
//     or block shutdown indefinitely.
//
// The canonical caller is the post-DB-failure cleanup of an object
// that was just written to a remote store: the request ctx has been
// returned to the client (and may be cancelled), but we still need a
// bounded window in which to delete the orphaned object.
//
// Callers MUST defer cancel(). A zero or negative d uses 5 seconds,
// which is the conservative default observed across the auth-api and
// flow-api object-store flows.
func Cleanup(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 5 * time.Second
	}
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), d)
}
