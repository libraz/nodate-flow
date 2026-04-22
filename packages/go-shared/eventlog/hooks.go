package eventlog

import (
	"context"
	"sync"
	"sync/atomic"
)

// globalSeq is a monotonically increasing counter that assigns a
// sequence number to every append notification. Subscribers can use
// the sequence to detect gaps if they relay events to clients.
var globalSeq atomic.Int64

var (
	hooksMu sync.RWMutex
	hooks   []NotifyHook
)

// RegisterHook appends a subscriber to the fan-out. Returns an index
// that can be passed to RemoveHook; tests use this to unregister.
func RegisterHook(h NotifyHook) int {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = append(hooks, h)
	return len(hooks) - 1
}

// ClearHooks drops every registered subscriber. Used by tests that
// want a clean slate between runs.
func ClearHooks() {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = nil
}

type seqCtxKey struct{}

// WithSeq returns a copy of ctx carrying the event sequence number.
// Hooks can retrieve it via SeqFromContext to include in their
// notifications, allowing clients to detect gaps and reorder.
func WithSeq(ctx context.Context, seq int64) context.Context {
	return context.WithValue(ctx, seqCtxKey{}, seq)
}

// SeqFromContext returns the sequence number set by Append, or zero
// if ctx was not tagged.
func SeqFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(seqCtxKey{}).(int64)
	return v
}

func fireHooks(ctx context.Context, workspaceID uint32, eventType string) {
	hooksMu.RLock()
	snapshot := hooks
	hooksMu.RUnlock()
	if len(snapshot) == 0 {
		return
	}
	seq := globalSeq.Add(1)
	ctx = WithSeq(ctx, seq)
	for _, h := range snapshot {
		h(ctx, workspaceID, eventType)
	}
}
