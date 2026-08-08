package calendar_event_day

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testJob() *Job {
	return &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}
}

func TestSpan_FirstTickUsesCatchUpWindow(t *testing.T) {
	t.Parallel()
	job := testJob()

	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	span := job.spanForWorkspace(context.Background(), 1, now)

	require.Equal(t, 26*time.Hour+time.Minute, span.width)
	require.Equal(t, now, span.upper)
}

func TestSpan_SteadyTickUsesElapsedIntervalOnly(t *testing.T) {
	t.Parallel()
	job := testJob()
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)

	span := job.spanSinceLast(last, last.Add(time.Minute))

	require.Equal(t, time.Minute, span.width,
		"steady-state ticks must not carry the 26h catch-up window")
	require.Equal(t, last.Add(time.Minute), span.upper)
}

func TestSpan_GapUsesElapsedTime(t *testing.T) {
	t.Parallel()
	job := testJob()
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)

	span := job.spanSinceLast(last, last.Add(3*time.Hour))

	require.Equal(t, 3*time.Hour, span.width,
		"a worker gap should scan the missed elapsed range")
	require.Equal(t, last.Add(3*time.Hour), span.upper,
		"a gap inside the allowance is cleared in one tick, so the cursor reaches now")
}

// The regression: a gap wider than the allowance used to be scanned
// backwards from `now` and the cursor still jumped to `now`, so the days
// in the middle were never scanned by any tick — not then, and not after
// the workspace recovered.
//
// One tick still scans at most the allowance. What changed is which
// slice: the oldest one, with the cursor advancing by exactly as much as
// was scanned, so the next tick continues from there.
func TestSpan_AGapWiderThanTheAllowanceIsWalkedNotSkipped(t *testing.T) {
	t.Parallel()
	job := testJob()
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	now := last.Add(72 * time.Hour)
	maxWindow := 26*time.Hour + time.Minute

	span := job.spanSinceLast(last, now)

	require.Equal(t, maxWindow, span.width,
		"one tick must stay bounded by the configured catch-up cap")
	require.Equal(t, last.Add(maxWindow), span.upper,
		"the tick must take the oldest slice of the gap; ending it at `now` is what dropped the days in between")
	require.True(t, span.upper.Before(now),
		"the workspace is still behind after this tick and must be scanned again")

	// Nothing between the old cursor and this span's lower bound is left
	// out: the span starts exactly where the cursor was.
	require.Equal(t, last, span.upper.Add(-span.width),
		"the scanned range must begin at the cursor, leaving no unscanned days behind it")
}

// Walking a long outage must terminate, and it must terminate having
// covered every instant between the cursor and the tick time.
func TestSpan_RepeatedTicksCatchUpWithoutLeavingAHole(t *testing.T) {
	t.Parallel()
	job := testJob()
	cursor := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	now := cursor.Add(10 * 24 * time.Hour)

	covered := cursor
	for i := 0; i < 100; i++ {
		span := job.spanSinceLast(cursor, now)
		require.Equal(t, covered, span.upper.Add(-span.width),
			"tick %d must resume where the previous one stopped", i)
		covered = span.upper
		cursor = span.upper
		if !cursor.Before(now) {
			break
		}
	}

	require.Equal(t, now, covered,
		"repeated ticks must reach the present; a 10 day outage may delay days but must not drop them")
}

func TestSpan_TracksCursorsPerWorkspace(t *testing.T) {
	t.Parallel()
	job := testJob()
	ctx := context.Background()
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, job.cursors().Save(ctx, 1, last))

	require.Equal(t, time.Minute, job.spanForWorkspace(ctx, 1, last.Add(time.Minute)).width,
		"workspace 1 should use its own cursor")
	require.Equal(t, 26*time.Hour+time.Minute, job.spanForWorkspace(ctx, 2, last.Add(time.Minute)).width,
		"workspace 2 must retain catch-up when it has not been scanned yet")
}

// A failed tick must leave the cursor alone. Advancing it past a span
// whose days were never materialised is the other half of how days went
// missing: the workspace that failed was also the one told it was done.
func TestCursorDoesNotAdvanceOnAFailedTick(t *testing.T) {
	t.Parallel()
	job := testJob()
	ctx := context.Background()
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, job.cursors().Save(ctx, 7, last))

	// The scan for workspace 7 fails, so markScanned is never reached.
	after, err := job.cursors().Load(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, last, after)

	// A successful tick moves it to the span's upper bound, not to now.
	now := last.Add(72 * time.Hour)
	span := job.spanSinceLast(last, now)
	job.markScanned(ctx, Workspace{ID: 7}, span.upper)

	moved, err := job.cursors().Load(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, span.upper, moved)
	require.True(t, moved.Before(now),
		"a catch-up tick must not claim the whole gap it did not scan")
}
