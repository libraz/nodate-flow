package stream

import (
	"context"
	"sync"
	"sync/atomic"
)

// Notifier is the per-process pub/sub that fans an [Event] out to
// every subscriber currently listening for that workspace. The
// interface exists so a future multi-replica implementation (Redis
// Pub/Sub, NATS, ...) can drop in without touching call sites.
type Notifier interface {
	// Publish delivers evt to every subscriber registered for
	// evt.WorkspaceID. It must never block on slow subscribers: if a
	// subscriber's inbox is full, the event is dropped for that
	// subscriber and a [KindResync] event is queued instead so the
	// client re-invalidates everything on reconnect.
	Publish(ctx context.Context, evt Event)

	// Subscribe returns a receive-only channel that yields events for
	// the given workspace until ctx is cancelled. Callers MUST read
	// from the channel promptly; lost events are converted to a
	// [KindResync] marker per the drop policy above.
	Subscribe(ctx context.Context, workspacePublicID string) <-chan Event
}

// NopNotifier is the zero value of an unset [Notifier]. Publish is a
// no-op and Subscribe returns a closed channel, so code paths that
// never set a real notifier (tests, CLI tools) see exactly the same
// behavior they did before ADR 0005.
type NopNotifier struct{}

// Publish is a no-op.
func (NopNotifier) Publish(context.Context, Event) {}

// Subscribe returns a closed channel.
func (NopNotifier) Subscribe(context.Context, string) <-chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}

// subscriberInboxSize is the buffered channel size per subscriber. A
// subscriber that has not drained its inbox within this window is
// considered "slow" and gets a [KindResync] marker instead of the
// next event.
const subscriberInboxSize = 64

// subscriber is one registered SSE stream.
type subscriber struct {
	workspaceID string
	ch          chan Event
	// resyncQueued is 1 when we have already dropped an event for
	// this subscriber and queued a resync marker; it prevents us
	// from flooding the resync queue under sustained pressure.
	resyncQueued atomic.Bool
}

// InProcessNotifier is the v1 single-replica [Notifier]. It keeps
// subscribers in a map keyed by workspace public id and fans out via
// non-blocking channel sends.
//
// It is safe for concurrent use from any number of goroutines.
type InProcessNotifier struct {
	mu          sync.RWMutex
	subscribers map[string]map[*subscriber]struct{}

	// Lightweight counters used by /metrics scrapers and tests.
	// Bumped under Publish with atomic operations so metric reads
	// never block the hot path.
	eventsPublished atomic.Uint64
	eventsDropped   atomic.Uint64
}

// MetricsSnapshot is a point-in-time view of notifier counters.
type MetricsSnapshot struct {
	EventsPublished    uint64
	EventsDropped      uint64
	ActiveWorkspaces   int
	ActiveSubscribers  int
}

// Snapshot returns a consistent read of the notifier's counters
// plus current subscriber totals. Intended for Prometheus wiring
// (obs package) and for tests that need to assert fan-out counts.
func (n *InProcessNotifier) Snapshot() MetricsSnapshot {
	n.mu.RLock()
	defer n.mu.RUnlock()
	total := 0
	for _, bucket := range n.subscribers {
		total += len(bucket)
	}
	return MetricsSnapshot{
		EventsPublished:   n.eventsPublished.Load(),
		EventsDropped:     n.eventsDropped.Load(),
		ActiveWorkspaces:  len(n.subscribers),
		ActiveSubscribers: total,
	}
}

// NewInProcessNotifier returns an empty notifier ready for use.
func NewInProcessNotifier() *InProcessNotifier {
	return &InProcessNotifier{
		subscribers: make(map[string]map[*subscriber]struct{}),
	}
}

// Publish fans evt out to every subscriber of evt.WorkspaceID. Slow
// subscribers are converted to a pending resync marker rather than
// blocking the caller. Publish returns as soon as every subscriber
// has been either delivered or marked.
func (n *InProcessNotifier) Publish(_ context.Context, evt Event) {
	n.mu.RLock()
	subs := n.subscribers[evt.WorkspaceID]
	list := make([]*subscriber, 0, len(subs))
	for s := range subs {
		list = append(list, s)
	}
	n.mu.RUnlock()

	for _, s := range list {
		// If a resync is already queued for this subscriber, skip
		// both the normal event and further resync markers: the
		// client is going to resync on reconnect anyway.
		if s.resyncQueued.Load() {
			continue
		}
		select {
		case s.ch <- evt:
			n.eventsPublished.Add(1)
		default:
			n.eventsDropped.Add(1)
			// Inbox full. Try to queue a resync marker exactly once.
			// The channel is still full, so drop one pending event
			// to make room: the resync marker is a superset of any
			// single lost event from the client's perspective.
			if s.resyncQueued.CompareAndSwap(false, true) {
				select {
				case <-s.ch:
				default:
				}
				select {
				case s.ch <- Event{
					Kind:        KindResync,
					WorkspaceID: evt.WorkspaceID,
					At:          evt.At,
				}:
				default:
					// Still full (racing drain failed). Give up
					// silently; the subscriber's context will expire
					// soon and the SSE handler will reconnect.
				}
			}
		}
	}
}

// Subscribe registers a new subscriber for workspacePublicID and
// returns its receive channel. The subscriber is removed and its
// channel closed when ctx is cancelled.
func (n *InProcessNotifier) Subscribe(ctx context.Context, workspacePublicID string) <-chan Event {
	s := &subscriber{
		workspaceID: workspacePublicID,
		ch:          make(chan Event, subscriberInboxSize),
	}

	n.mu.Lock()
	bucket, ok := n.subscribers[workspacePublicID]
	if !ok {
		bucket = make(map[*subscriber]struct{})
		n.subscribers[workspacePublicID] = bucket
	}
	bucket[s] = struct{}{}
	n.mu.Unlock()

	go func() {
		<-ctx.Done()
		n.mu.Lock()
		if bucket, ok := n.subscribers[workspacePublicID]; ok {
			delete(bucket, s)
			if len(bucket) == 0 {
				delete(n.subscribers, workspacePublicID)
			}
		}
		n.mu.Unlock()
		close(s.ch)
	}()

	return s.ch
}

// ActiveSubscribers returns the number of registered subscribers for
// workspacePublicID. Intended for metrics and tests.
func (n *InProcessNotifier) ActiveSubscribers(workspacePublicID string) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.subscribers[workspacePublicID])
}
