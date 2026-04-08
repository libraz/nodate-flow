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
	rows    []OnEventAgentMatch
}

func (f *fakeOnEventQuerier) ListOnEventAgentsFor(_ context.Context, ws uint32, kind string) ([]OnEventAgentMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWs = ws
	f.lastKnd = kind
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

	hook(context.Background(), 1, "signal.attached")
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
	et.NotifyHook()(context.Background(), 1, "task.updated")
	// Claim with a cancelled ctx should error immediately since the
	// buffer is empty.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queue.Claim(ctx)
	require.Error(t, err)
}
