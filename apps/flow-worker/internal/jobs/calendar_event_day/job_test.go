package calendar_event_day

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDayWindow_FirstTickUsesCatchUpWindow(t *testing.T) {
	t.Parallel()
	job := &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}

	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.Equal(t, 26*time.Hour+time.Minute, job.dayWindow(now))
}

func TestDayWindow_SteadyTickUsesElapsedIntervalOnly(t *testing.T) {
	t.Parallel()
	job := &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	job.markSuccessfulTick(last)

	require.Equal(t, time.Minute, job.dayWindow(last.Add(time.Minute)),
		"steady-state ticks must not carry the 26h catch-up window")
}

func TestDayWindow_GapUsesElapsedTime(t *testing.T) {
	t.Parallel()
	job := &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	job.markSuccessfulTick(last)

	require.Equal(t, 3*time.Hour, job.dayWindow(last.Add(3*time.Hour)),
		"a worker gap should scan the missed elapsed range")
}

func TestDayWindow_GapIsCappedByCatchUpWindow(t *testing.T) {
	t.Parallel()
	job := &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	job.markSuccessfulTick(last)

	require.Equal(t, 26*time.Hour+time.Minute, job.dayWindow(last.Add(72*time.Hour)),
		"large outages should stay bounded by the configured catch-up cap")
}

func TestDayWindowTracksSuccessfulTicksPerWorkspace(t *testing.T) {
	t.Parallel()
	job := &Job{
		TickInterval:  60 * time.Second,
		CatchUpWindow: 26 * time.Hour,
	}
	last := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	job.markWorkspaceSuccessfulTick(1, last)

	require.Equal(t, time.Minute, job.dayWindowForWorkspace(1, last.Add(time.Minute)),
		"workspace 1 should use its own steady-state cursor")
	require.Equal(t, 26*time.Hour+time.Minute, job.dayWindowForWorkspace(2, last.Add(time.Minute)),
		"workspace 2 must retain catch-up when it has not succeeded yet")
}
