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

// Subscribers are keyed by an ever-increasing handle rather than held
// in a slice, so a handle stays valid however the set changes around
// it. A slice position does not: it shifts when an earlier subscriber
// goes away, and after [ClearHooks] every outstanding one addresses a
// different subscriber or none. The same layout as flow-api's
// eventbus registry, for the same reason.
var (
	hooksMu   sync.RWMutex
	hooks     = map[int]NotifyHook{}
	hookSeq   int
	hookOrder []int
)

// RegisterHook adds a subscriber to the fan-out and returns the handle
// that unregisters it through [RemoveHook]. A nil hook is ignored and
// yields a handle that removes nothing.
func RegisterHook(h NotifyHook) int {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hookSeq++
	handle := hookSeq
	if h == nil {
		return handle
	}
	hooks[handle] = h
	hookOrder = append(hookOrder, handle)
	return handle
}

// RemoveHook unregisters the subscriber registered under handle.
// Unknown or already-removed handles are ignored, so tearing down twice
// is safe.
//
// Removing one subscriber rather than calling [ClearHooks] is what a
// test with a temporary subscriber wants: the process-wide bridge that
// forwards these appends to flow-api's subscribers is registered here
// too, and clearing takes it down with everything else.
func RemoveHook(handle int) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	delete(hooks, handle)
	for i, h := range hookOrder {
		if h == handle {
			hookOrder = append(hookOrder[:i:i], hookOrder[i+1:]...)
			break
		}
	}
}

// ClearHooks drops every registered subscriber. Used by tests that
// want a clean slate between runs.
func ClearHooks() {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = map[int]NotifyHook{}
	hookOrder = nil
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

// fireHooks dispatches to every subscriber in registration order.
//
// The snapshot is taken under the lock and dispatched outside it, so a
// subscriber is free to register or remove one during its own call
// without deadlocking, and a subscriber added mid-dispatch does not see
// the event that was already in flight.
//
// An empty registry is the normal case in auth-api, which writes member
// events but consumes none, so the empty snapshot returns without
// touching the sequence counter.
func fireHooks(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	hooksMu.RLock()
	snapshot := make([]NotifyHook, 0, len(hookOrder))
	for _, handle := range hookOrder {
		if h, ok := hooks[handle]; ok {
			snapshot = append(snapshot, h)
		}
	}
	hooksMu.RUnlock()
	if len(snapshot) == 0 {
		return
	}
	seq := globalSeq.Add(1)
	ctx = WithSeq(ctx, seq)
	for _, h := range snapshot {
		h(ctx, workspaceID, eventType, eventInternalID)
	}
}
