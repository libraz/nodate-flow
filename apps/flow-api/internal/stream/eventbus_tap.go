package stream

import (
	"context"
	"sync"
	"time"
)

// EventbusTap is the bridge from package eventbus to a [Notifier].
// eventbus.Append deliberately knows nothing about streaming, so it
// calls a package-level hook that the server wires to an instance of
// this tap at startup.
//
// The tap accepts the internal workspace id that eventbus.Append
// already has on hand and looks up the matching public id via an
// in-memory cache populated lazily by the SSE handler subscriptions.
// This keeps eventbus free of DB lookups on the hot path.
type EventbusTap struct {
	notifier Notifier

	mu    sync.RWMutex
	cache map[uint32]string // internal workspace id → public id
}

// NewEventbusTap returns a tap writing to notifier.
func NewEventbusTap(notifier Notifier) *EventbusTap {
	if notifier == nil {
		notifier = NopNotifier{}
	}
	return &EventbusTap{
		notifier: notifier,
		cache:    make(map[uint32]string),
	}
}

// RememberWorkspace caches the internal→public id mapping for a
// workspace. The SSE handler calls this on every new subscription so
// the tap can resolve the public id without hitting the database.
func (t *EventbusTap) RememberWorkspace(internalID uint32, publicID string) {
	t.mu.Lock()
	t.cache[internalID] = publicID
	t.mu.Unlock()
}

// Publish is the hook eventbus.Append calls after a successful
// insert. It looks up the workspace public id, maps the event type
// to a stream Kind, and forwards to the notifier. Unknown event
// types and workspaces not yet in the cache are silently dropped:
// the frontend will do the right thing on the next subscription
// because the SSE handler always writes an initial resync marker.
//
// eventInternalID is unused here; the SSE tap signals "something
// happened" without carrying the events.id forward.
func (t *EventbusTap) Publish(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint32) {
	_ = eventInternalID
	kind, ok := KindForEventType(eventType)
	if !ok {
		return
	}
	t.mu.RLock()
	publicID, cached := t.cache[workspaceInternalID]
	t.mu.RUnlock()
	if !cached {
		return
	}
	t.notifier.Publish(ctx, Event{
		Kind:        kind,
		WorkspaceID: publicID,
		At:          time.Now().Unix(),
	})
}

// PublishAiInvocation is a dedicated entry point for ai_invocations
// writes, which do not go through eventbus.Append. Call it from the
// ai provider layer right after a new invocation row is inserted.
func (t *EventbusTap) PublishAiInvocation(ctx context.Context, workspaceInternalID uint32) {
	t.mu.RLock()
	publicID, cached := t.cache[workspaceInternalID]
	t.mu.RUnlock()
	if !cached {
		return
	}
	t.notifier.Publish(ctx, Event{
		Kind:        KindAiInvocationWritten,
		WorkspaceID: publicID,
		At:          time.Now().Unix(),
	})
}
