package stream

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// wsCacheLimit bounds the internal→public workspace id cache. The
// mapping is immutable, so entries are only ever dropped to keep the
// map from being a permanent record of every workspace the process has
// ever published for; the oldest half goes when the limit is reached.
const wsCacheLimit = 4096

// WorkspaceResolver answers "what is this workspace's public id?" for a
// workspace this process has no local subscriber for. It is the tap's
// fallback when the id is not already cached.
type WorkspaceResolver interface {
	PublicWorkspaceID(ctx context.Context, internalID uint32) (string, error)
}

// EventbusTap is the bridge from package eventbus to a [Notifier].
// eventbus.Append deliberately knows nothing about streaming, so it
// calls a package-level hook that the server wires to an instance of
// this tap at startup.
//
// The tap accepts the internal workspace id that eventbus.Append
// already has on hand and resolves the matching public id from an
// in-memory cache, falling back to a [WorkspaceResolver] on a miss.
// The cache is what keeps eventbus free of a DB lookup on the hot path
// in the case that matters — a workspace this replica publishes for
// repeatedly.
type EventbusTap struct {
	notifier Notifier
	resolver WorkspaceResolver

	mu    sync.RWMutex
	cache map[uint32]string // internal workspace id → public id

	// own records the events.id values this tap published, so a
	// [Tailer] reading the same log can tell them from another
	// process's appends and not invalidate twice.
	own selfWrites
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

// SetWorkspaceResolver installs the fallback lookup used when a
// workspace has never been seen by this process.
//
// Without one, the tap could only publish for workspaces some local
// SSE subscriber had already taught it about. That is invisible in a
// single-replica deployment — the subscriber and the writer are the
// same process — and wrong in every other one: on a replica handling
// the write but holding none of the subscriptions, the event was
// dropped before it ever reached the fan-out. With the Redis notifier
// that is the whole delivery path, and with NF_FLOW_STREAM_TAIL off
// there is no second path to cover for it.
func (t *EventbusTap) SetWorkspaceResolver(r WorkspaceResolver) {
	t.mu.Lock()
	t.resolver = r
	t.mu.Unlock()
}

// trackSelfWrites switches on the self-write ledger. [NewTailer] calls
// it; nothing else should, because the ledger is only bounded by a
// tailer draining it.
func (t *EventbusTap) trackSelfWrites() { t.own.enable() }

// claim implements [selfWriteLedger].
func (t *EventbusTap) claim(id uint64) bool { return t.own.claim(id) }

// RememberWorkspace caches the internal→public id mapping for a
// workspace. The SSE handler calls this on every new subscription so
// the tap can resolve the public id without hitting the database.
func (t *EventbusTap) RememberWorkspace(internalID uint32, publicID string) {
	t.mu.Lock()
	t.remember(internalID, publicID)
	t.mu.Unlock()
}

// remember records one mapping. Caller holds the write lock.
func (t *EventbusTap) remember(internalID uint32, publicID string) {
	if len(t.cache) >= wsCacheLimit {
		// Drop half the entries rather than growing without bound. Any
		// dropped mapping is re-resolved on its next event; the cost of
		// being wrong here is one extra query, so this is the cheap
		// direction to fail in.
		dropped := 0
		for id := range t.cache {
			delete(t.cache, id)
			dropped++
			if dropped >= wsCacheLimit/2 {
				break
			}
		}
	}
	t.cache[internalID] = publicID
}

// workspacePublicID returns the public id for an internal workspace id,
// consulting the cache first and the resolver on a miss. An id that
// cannot be resolved yields ("", false) and the caller drops the event:
// there is nothing to address it to.
func (t *EventbusTap) workspacePublicID(ctx context.Context, internalID uint32) (string, bool) {
	t.mu.RLock()
	publicID, cached := t.cache[internalID]
	resolver := t.resolver
	t.mu.RUnlock()
	if cached {
		return publicID, true
	}
	if resolver == nil {
		return "", false
	}
	resolved, err := resolver.PublicWorkspaceID(ctx, internalID)
	if err != nil || resolved == "" {
		return "", false
	}
	t.mu.Lock()
	t.remember(internalID, resolved)
	t.mu.Unlock()
	return resolved, true
}

// Publish is the hook eventbus.Append calls after a successful
// insert. It resolves the workspace public id, maps the event type
// to a stream Kind, and forwards to the notifier. Unknown event
// types are silently dropped: the frontend will do the right thing on
// the next subscription because the SSE handler always writes an
// initial resync marker.
//
// eventInternalID does not reach the wire — the SSE payload is
// invalidation-only — but it is recorded so a [Tailer] reading the
// same log can skip what this tap has already delivered. Only ids that
// were actually published are recorded: an event dropped here for an
// unknown kind is dropped by the tailer too.
func (t *EventbusTap) Publish(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint64) {
	kind, ok := KindForEventType(eventType)
	if !ok {
		return
	}
	publicID, ok := t.workspacePublicID(ctx, workspaceInternalID)
	if !ok {
		return
	}
	t.own.record(eventInternalID)
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
	publicID, ok := t.workspacePublicID(ctx, workspaceInternalID)
	if !ok {
		return
	}
	t.notifier.Publish(ctx, Event{
		Kind:        KindAiInvocationWritten,
		WorkspaceID: publicID,
		At:          time.Now().Unix(),
	})
}

// PublishNotification is a dedicated entry point for notifications
// writes, which the fan-out performs outside eventbus.Append. Call it
// from the notification fan-out once a pass has written at least one
// row.
//
// One call covers the whole pass. [Event] addresses a workspace, not a
// reader — the notifier keys subscribers by workspace public id alone —
// so the many rows one event produces across recipients are one thing to
// say, and saying it per row would only repeat it. The converse is the
// caller's obligation: a pass that wrote nothing must not call this, or
// every client in the workspace refetches a bell that did not change.
func (t *EventbusTap) PublishNotification(ctx context.Context, workspaceInternalID uint32) {
	publicID, ok := t.workspacePublicID(ctx, workspaceInternalID)
	if !ok {
		return
	}
	t.notifier.Publish(ctx, Event{
		Kind:        KindNotificationChanged,
		WorkspaceID: publicID,
		At:          time.Now().Unix(),
	})
}

// DBWorkspaceResolver reads the public id straight from the workspaces
// table. The mapping never changes, so a hit is cached for the life of
// the process and this runs at most once per workspace per replica
// (plus once more for anything the cache had to evict).
type DBWorkspaceResolver struct {
	DB *sql.DB
	// Timeout caps the lookup so a slow database cannot hold up the
	// caller that just appended an event. Zero uses one second.
	Timeout time.Duration
}

// PublicWorkspaceID implements [WorkspaceResolver].
func (r *DBWorkspaceResolver) PublicWorkspaceID(ctx context.Context, internalID uint32) (string, error) {
	if r == nil || r.DB == nil {
		return "", sql.ErrNoRows
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	// The caller's context belongs to a request that may already be
	// finishing; the append it is reporting has been committed either
	// way, so the lookup runs on its own deadline rather than inheriting
	// a cancellation that has nothing to do with it.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	var publicID string
	err := r.DB.QueryRowContext(lookupCtx,
		`SELECT BIN_TO_UUID(public_id, 0) FROM workspaces WHERE id = ?`,
		internalID,
	).Scan(&publicID)
	if err != nil {
		return "", err
	}
	return publicID, nil
}
