package stream

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// countingResolver answers workspace lookups from a fixed table and
// records how many times it was asked, so a test can tell a cached
// answer from a fresh one.
type countingResolver struct {
	byID  map[uint32]string
	calls atomic.Int64
}

func (r *countingResolver) PublicWorkspaceID(_ context.Context, internalID uint32) (string, error) {
	r.calls.Add(1)
	if pub, ok := r.byID[internalID]; ok {
		return pub, nil
	}
	return "", errors.New("no such workspace")
}

// The replica that handles the write is not necessarily the one holding
// the subscriptions. Publishing only for workspaces a local subscriber
// had already registered meant that on every other replica the event
// was dropped before it reached the fan-out — and with the Redis
// notifier, that fan-out is the whole delivery path.
func TestTapPublishesForAWorkspaceWithNoLocalSubscriber(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tap := NewEventbusTap(rec)
	tap.SetWorkspaceResolver(&countingResolver{byID: map[uint32]string{7: "ws-public-7"}})

	tap.Publish(context.Background(), 7, "task.created", 1)

	if len(rec.events) != 1 {
		t.Fatalf("published %d events, want 1: the write replica dropped it", len(rec.events))
	}
	if rec.events[0].WorkspaceID != "ws-public-7" {
		t.Errorf("workspace = %q, want %q", rec.events[0].WorkspaceID, "ws-public-7")
	}
	if rec.events[0].Kind != KindTaskChanged {
		t.Errorf("kind = %q, want %q", rec.events[0].Kind, KindTaskChanged)
	}
}

// The same holds for the ai_invocations path, which does not go
// through eventbus.Append and so has its own entry point.
func TestTapPublishesAiInvocationWithNoLocalSubscriber(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tap := NewEventbusTap(rec)
	tap.SetWorkspaceResolver(&countingResolver{byID: map[uint32]string{9: "ws-public-9"}})

	tap.PublishAiInvocation(context.Background(), 9)

	if len(rec.events) != 1 {
		t.Fatalf("published %d events, want 1", len(rec.events))
	}
	if rec.events[0].Kind != KindAiInvocationWritten {
		t.Errorf("kind = %q, want %q", rec.events[0].Kind, KindAiInvocationWritten)
	}
}

// The resolver is the fallback, not the path: a workspace whose id is
// already known must not cost a query per event.
func TestTapResolvesEachWorkspaceOnce(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	res := &countingResolver{byID: map[uint32]string{4: "ws-public-4"}}
	tap := NewEventbusTap(rec)
	tap.SetWorkspaceResolver(res)

	for i := range 5 {
		tap.Publish(context.Background(), 4, "task.updated", uint64(i+1))
	}

	if len(rec.events) != 5 {
		t.Fatalf("published %d events, want 5", len(rec.events))
	}
	if got := res.calls.Load(); got != 1 {
		t.Errorf("resolver called %d times, want 1: the mapping is immutable and must be cached", got)
	}
}

// A subscription already taught the tap this mapping, so the fallback
// must not be consulted at all.
func TestTapPrefersTheRememberedMapping(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	res := &countingResolver{byID: map[uint32]string{4: "from-resolver"}}
	tap := NewEventbusTap(rec)
	tap.SetWorkspaceResolver(res)
	tap.RememberWorkspace(4, "from-subscription")

	tap.Publish(context.Background(), 4, "task.updated", 1)

	if len(rec.events) != 1 || rec.events[0].WorkspaceID != "from-subscription" {
		t.Fatalf("events = %+v, want one addressed to from-subscription", rec.events)
	}
	if got := res.calls.Load(); got != 0 {
		t.Errorf("resolver called %d times, want 0", got)
	}
}

// An unresolvable workspace has no address to publish to, so the event
// is dropped rather than sent somewhere arbitrary.
func TestTapDropsAnUnresolvableWorkspace(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	tap := NewEventbusTap(rec)
	tap.SetWorkspaceResolver(&countingResolver{byID: map[uint32]string{}})

	tap.Publish(context.Background(), 99, "task.created", 1)

	if len(rec.events) != 0 {
		t.Fatalf("published %d events for an unknown workspace, want 0", len(rec.events))
	}
}

// The production resolver has to agree with what the rest of the
// system calls a workspace public id, which only the database can say.
func TestDBWorkspaceResolverReadsThePublicID(t *testing.T) {
	db := tailDB(t)
	wsID, wsPub := seedWorkspace(t, db, "tap-resolver")

	res := &DBWorkspaceResolver{DB: db}
	got, err := res.PublicWorkspaceID(context.Background(), wsID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != wsPub {
		t.Errorf("public id = %q, want %q", got, wsPub)
	}

	if _, err := res.PublicWorkspaceID(context.Background(), 0); err == nil {
		t.Error("resolving a workspace that does not exist should fail")
	}
}

// The cache is keyed by workspace and a long-lived process sees many of
// them, so it needs a ceiling — otherwise it is a permanent record of
// every workspace this replica has ever published for.
func TestTapWorkspaceCacheIsBounded(t *testing.T) {
	t.Parallel()

	tap := NewEventbusTap(&recorder{})
	for i := range wsCacheLimit * 2 {
		//#nosec G115 -- loop bound is a small constant
		tap.RememberWorkspace(uint32(i+1), fmt.Sprintf("ws-%d", i+1))
	}

	tap.mu.RLock()
	size := len(tap.cache)
	tap.mu.RUnlock()
	if size > wsCacheLimit {
		t.Fatalf("cache holds %d entries, want at most %d", size, wsCacheLimit)
	}
}
