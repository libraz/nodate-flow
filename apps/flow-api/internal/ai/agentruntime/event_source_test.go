package agentruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeOnEventQuerier struct {
	mu      sync.Mutex
	lastWs  uint32
	lastKnd string
	lastEid uint64
	calls   int
	rows    []OnEventAgentMatch
}

func (f *fakeOnEventQuerier) ListOnEventAgentsFor(_ context.Context, ws uint32, kind string) ([]OnEventAgentMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWs = ws
	f.lastKnd = kind
	f.calls++
	return f.rows, nil
}

func (f *fakeOnEventQuerier) ListOnEventAgentsForEvent(_ context.Context, ws uint32, eid uint64) ([]OnEventAgentMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastWs = ws
	f.lastEid = eid
	f.calls++
	return f.rows, nil
}

// snapshot reads the recorded arguments under the lock. The dispatch
// runs on its own goroutine now, so the test may not touch these
// fields directly.
func (f *fakeOnEventQuerier) snapshot() (ws uint32, kind string, eid uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastWs, f.lastKnd, f.lastEid
}

// waitForCall blocks until the trigger has performed its lookup. The
// hook returns before the work happens — that is the point of it — so
// every assertion about what the dispatch did has to wait for it
// rather than assume it already ran.
func (f *fakeOnEventQuerier) waitForCall(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		n := f.calls
		f.mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the trigger never looked up on_event agents")
		}
		time.Sleep(time.Millisecond)
	}
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

	// Claim blocks until the detached dispatch enqueues, so the reads
	// below observe a finished dispatch without a sleep.
	got1, err := queue.Claim(context.Background())
	require.NoError(t, err)
	got2, err := queue.Claim(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]uint32{got1.Job.AgentID, got2.Job.AgentID},
		[]uint32{10, 11},
	)
	require.Contains(t, got1.DedupeKey, ":event:signal.attached:")

	_, kind, _ := q.snapshot()
	require.Equal(t, "signal.attached", kind)
}

// TestEventTriggerNoMatches confirms an empty row set simply no-ops
// without touching the queue or panicking.
func TestEventTriggerNoMatches(t *testing.T) {
	t.Parallel()
	q := &fakeOnEventQuerier{rows: nil}
	queue := NewInProcessQueue(4)
	et := &EventTrigger{Queries: q, Queue: queue}
	et.NotifyHook()(context.Background(), 1, "task.updated", 0)
	q.waitForCall(t)
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
			q.waitForCall(t)

			_, kind, eid := q.snapshot()
			if tc.wantScopedHit {
				require.Equal(t, tc.eventID, eid, "scoped path should pass eventInternalID to query")
				require.Empty(t, kind, "legacy event-type path must not be used")
			} else {
				require.Equal(t, "task.updated", kind, "legacy path should pass event type")
				require.Equal(t, uint64(0), eid)
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

// blockingQuerier holds its lookup until released, modelling a slow or
// stuck database.
type blockingQuerier struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (b *blockingQuerier) block() {
	b.once.Do(func() { close(b.entered) })
	<-b.release
}

func (b *blockingQuerier) ListOnEventAgentsFor(context.Context, uint32, string) ([]OnEventAgentMatch, error) {
	b.block()
	return nil, nil
}

func (b *blockingQuerier) ListOnEventAgentsForEvent(context.Context, uint32, uint64) ([]OnEventAgentMatch, error) {
	b.block()
	return nil, nil
}

// TestNotifyHookDoesNotBlockCaller is the regression for inline hook work. The hook
// runs on the goroutine that appended the event — a request handler, or
// the commit hook of its transaction — so any work it does inline is
// work the user waits for. A workspace with on_event agents would make
// every write in that workspace pay for an agent lookup plus an INSERT
// per match, and a slow lookup would stall the request that triggered
// it.
//
// The hook must return while the lookup is still in progress.
func TestNotifyHookDoesNotBlockCaller(t *testing.T) {
	t.Parallel()
	q := &blockingQuerier{release: make(chan struct{}), entered: make(chan struct{})}
	et := &EventTrigger{Queries: q, Queue: NewInProcessQueue(4)}

	returned := make(chan struct{})
	go func() {
		et.NotifyHook()(context.Background(), 1, "task.updated", 0)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("the notify hook must return without waiting for the agent lookup")
	}

	// And the work really is under way, so the hook is detaching it
	// rather than skipping it.
	select {
	case <-q.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the dispatch must still run, just not on the caller's goroutine")
	}
	close(q.release)

	// Shutdown drains it, so the process does not exit mid-dispatch.
	require.NoError(t, et.Shutdown(context.Background()))
}

// TestDispatchConcurrencyIsBounded pins the limit on how many
// dispatches touch the database at once. Goroutines are cheap and
// pooled connections are not: a burst of events must park rather than
// drain the pool the request handlers share.
func TestDispatchConcurrencyIsBounded(t *testing.T) {
	t.Parallel()
	q := &blockingQuerier{release: make(chan struct{}), entered: make(chan struct{})}
	var inFlight, peak int32
	counting := &countingQuerier{inner: q, inFlight: &inFlight, peak: &peak}

	et := &EventTrigger{Queries: counting, Queue: NewInProcessQueue(64), DispatchConcurrency: 2}
	hook := et.NotifyHook()
	for i := 0; i < 8; i++ {
		hook(context.Background(), 1, "task.updated", 0)
	}

	<-q.entered
	// Give the parked goroutines a chance to break the limit if the
	// semaphore is not doing its job.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Fatalf("at most 2 dispatches may hold a connection, peaked at %d", got)
	}
	close(q.release)
	require.NoError(t, et.Shutdown(context.Background()))
}

// countingQuerier records how many lookups are in flight at once.
type countingQuerier struct {
	inner    OnEventAgentsQuerier
	inFlight *int32
	peak     *int32
}

func (c *countingQuerier) track() func() {
	n := atomic.AddInt32(c.inFlight, 1)
	for {
		p := atomic.LoadInt32(c.peak)
		if n <= p || atomic.CompareAndSwapInt32(c.peak, p, n) {
			break
		}
	}
	return func() { atomic.AddInt32(c.inFlight, -1) }
}

func (c *countingQuerier) ListOnEventAgentsFor(ctx context.Context, ws uint32, kind string) ([]OnEventAgentMatch, error) {
	defer c.track()()
	return c.inner.ListOnEventAgentsFor(ctx, ws, kind)
}

func (c *countingQuerier) ListOnEventAgentsForEvent(ctx context.Context, ws uint32, eid uint64) ([]OnEventAgentMatch, error) {
	defer c.track()()
	return c.inner.ListOnEventAgentsForEvent(ctx, ws, eid)
}
