package agentruntime

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

type fakePurger struct {
	mu      sync.Mutex
	calls   int
	lastCut time.Time

	// Recorded arguments and canned results for the stranded-run
	// reaper.
	requeueCut      time.Time
	requeueAttempts uint8
	requeued        int64
	failCut         time.Time
	failAttempts    uint8
	failReason      string
	failedRows      int64
}

func (f *fakePurger) PurgeFinishedAgentRuns(_ context.Context, cut sql.NullTime) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if cut.Valid {
		f.lastCut = cut.Time
	}
	return nil
}

func (f *fakePurger) RequeueStrandedAgentRuns(_ context.Context, arg generated.RequeueStrandedAgentRunsParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if arg.ClaimedAt.Valid {
		f.requeueCut = arg.ClaimedAt.Time
	}
	f.requeueAttempts = arg.Attempts
	return f.requeued, nil
}

func (f *fakePurger) FailExhaustedAgentRuns(_ context.Context, arg generated.FailExhaustedAgentRunsParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if arg.ClaimedAt.Valid {
		f.failCut = arg.ClaimedAt.Time
	}
	f.failAttempts = arg.Attempts
	f.failReason = arg.ErrorMessage.String
	return f.failedRows, nil
}

func (f *fakePurger) snapshot() (int, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastCut
}

// TestPurgerCutoff verifies the cutoff handed to the query is
// now - retention, computed from the injected clock.
func TestPurgerCutoff(t *testing.T) {
	t.Parallel()
	q := &fakePurger{}
	fixed := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	p := &Purger{
		Queries:   q,
		Interval:  50 * time.Millisecond,
		Retention: 72 * time.Hour,
		Now:       func() time.Time { return fixed },
	}
	// Call tick directly so the test does not race the goroutine.
	p.tick(context.Background())
	calls, cut := q.snapshot()
	require.Equal(t, 1, calls)
	require.Equal(t, fixed.Add(-72*time.Hour), cut)
}

// TestPurgerLoopTicks verifies Start/Stop drives at least two passes
// within a short window and that Stop unblocks.
func TestPurgerLoopTicks(t *testing.T) {
	t.Parallel()
	q := &fakePurger{}
	p := &Purger{
		Queries:   q,
		Interval:  20 * time.Millisecond,
		Retention: time.Hour,
	}
	require.NoError(t, p.Start(context.Background()))
	time.Sleep(100 * time.Millisecond)
	p.Stop()
	calls, _ := q.snapshot()
	require.GreaterOrEqual(t, calls, 2, "expected the purger loop to fire at least twice")
}

// TestPurgerReapsStrandedRuns pins the recovery of runs abandoned in
// 'claimed'.
//
// A worker that dies mid-run leaves the row claimed forever. Nothing
// scans that status, and the scheduler's dedupe key is still held by the
// row, so the agent is never enqueued again either: one killed worker
// silently retires the agents it happened to be holding.
//
// The cutoff and the retry budget are both asserted because they are the
// two judgement calls. The budget matters more here than for webhook
// deliveries — every attempt is a model call — so a run that keeps
// stranding is failed rather than retried forever.
func TestPurgerReapsStrandedRuns(t *testing.T) {
	t.Parallel()
	q := &fakePurger{requeued: 2, failedRows: 1}
	fixed := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	p := &Purger{
		Queries:       q,
		Retention:     72 * time.Hour,
		StrandedAfter: 20 * time.Minute,
		MaxAttempts:   4,
		Now:           func() time.Time { return fixed },
	}

	p.tick(context.Background())

	q.mu.Lock()
	defer q.mu.Unlock()
	want := fixed.Add(-20 * time.Minute)
	require.Equal(t, want, q.requeueCut, "requeue cutoff must be now - StrandedAfter")
	require.Equal(t, want, q.failCut, "fail cutoff must use the same threshold")
	require.Equal(t, uint8(4), q.requeueAttempts, "the retry budget must reach the requeue query")
	require.Equal(t, uint8(4), q.failAttempts, "the same budget decides which runs are failed")
	require.NotEmpty(t, q.failReason, "a failed run must record why, or it is another silent stop")
}

// TestPurgerReapDefaults pins the defaults, which are the values that
// actually run in production.
func TestPurgerReapDefaults(t *testing.T) {
	t.Parallel()
	q := &fakePurger{}
	fixed := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	p := &Purger{Queries: q, Retention: time.Hour, Now: func() time.Time { return fixed }}

	p.tick(context.Background())

	q.mu.Lock()
	defer q.mu.Unlock()
	require.Equal(t, fixed.Add(-DefaultStrandedAfter), q.requeueCut)
	require.Equal(t, uint8(DefaultMaxAttempts), q.requeueAttempts)
}
