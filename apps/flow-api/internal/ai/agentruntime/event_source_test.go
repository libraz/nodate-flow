package agentruntime

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeOnEventQuerier struct {
	mu      sync.Mutex
	lastWs  uint32
	lastKnd string
	lastEid uint64
	rows    []OnEventAgentMatch
}

func (f *fakeOnEventQuerier) ListOnEventAgentsFor(_ context.Context, ws uint32, kind string) ([]OnEventAgentMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWs = ws
	f.lastKnd = kind
	return f.rows, nil
}

func (f *fakeOnEventQuerier) ListOnEventAgentsForEvent(_ context.Context, ws uint32, eid uint64) ([]OnEventAgentMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWs = ws
	f.lastEid = eid
	return f.rows, nil
}

// TestEventTriggerFanOut verifies one eventbus notify fires one
// Enqueue per matching agent, with dedupe keys scoped to the event
// kind so two different events do not collide.
func TestEventTriggerFanOut(t *testing.T) {
	t.Parallel()
	q := &fakeOnEventQuerier{rows: []OnEventAgentMatch{
		{ID: 10, WorkspaceID: 1},
		{ID: 11, WorkspaceID: 1},
	}}
	queue := NewInProcessQueue(16)
	et := &EventTrigger{Queries: q, Queue: queue}
	hook := et.NotifyHook()

	hook(context.Background(), 1, "signal.attached", 0)
	require.Equal(t, "signal.attached", q.lastKnd)

	got1, err := queue.Claim(context.Background())
	require.NoError(t, err)
	got2, err := queue.Claim(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]uint32{got1.Job.AgentID, got2.Job.AgentID},
		[]uint32{10, 11},
	)
	require.Contains(t, got1.DedupeKey, ":event:signal.attached:")
}

// TestEventTriggerNoMatches confirms an empty row set simply no-ops
// without touching the queue or panicking.
func TestEventTriggerNoMatches(t *testing.T) {
	t.Parallel()
	q := &fakeOnEventQuerier{rows: nil}
	queue := NewInProcessQueue(4)
	et := &EventTrigger{Queries: q, Queue: queue}
	et.NotifyHook()(context.Background(), 1, "task.updated", 0)
	// Claim with a cancelled ctx should error immediately since the
	// buffer is empty.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queue.Claim(ctx)
	require.Error(t, err)
}

// TestEventTriggerScopedDispatch covers the schedule_scope routing
// inside EventTrigger.dispatch: when eventInternalID != 0 the lookup
// goes through ListOnEventAgentsForEvent (which the production query
// filters by ai_agents.schedule_scope + task_actors membership);
// otherwise the legacy ListOnEventAgentsFor path is used. The fake
// querier just records which method was called so we can assert the
// routing decision without a real DB.
func TestEventTriggerScopedDispatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		eventID       uint64
		wantScopedHit bool
	}{
		{"scoped path used when event id known", 4242, true},
		{"legacy path used when event id is zero", 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &fakeOnEventQuerier{rows: []OnEventAgentMatch{{ID: 1, WorkspaceID: 9}}}
			queue := NewInProcessQueue(4)
			et := &EventTrigger{Queries: q, Queue: queue}
			et.NotifyHook()(context.Background(), 9, "task.updated", tc.eventID)

			q.mu.Lock()
			defer q.mu.Unlock()
			if tc.wantScopedHit {
				require.Equal(t, tc.eventID, q.lastEid, "scoped path should pass eventInternalID to query")
				require.Empty(t, q.lastKnd, "legacy event-type path must not be used")
			} else {
				require.Equal(t, "task.updated", q.lastKnd, "legacy path should pass event type")
				require.Equal(t, uint64(0), q.lastEid)
			}
		})
	}

	// Smoke-check the run that does get enqueued has SourceEventID
	// stamped so downstream task_id resolution can succeed.
	q := &fakeOnEventQuerier{rows: []OnEventAgentMatch{{ID: 1, WorkspaceID: 9}}}
	queue := NewInProcessQueue(4)
	et := &EventTrigger{Queries: q, Queue: queue}
	et.NotifyHook()(context.Background(), 9, "task.updated", 4242)
	got, err := queue.Claim(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(4242), got.Job.SourceEventID)
}
