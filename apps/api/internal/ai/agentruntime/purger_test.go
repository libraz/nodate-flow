package agentruntime

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakePurger struct {
	mu      sync.Mutex
	calls   int
	lastCut time.Time
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
