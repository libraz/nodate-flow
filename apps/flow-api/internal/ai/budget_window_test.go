package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// TestDailyBudgetBoundaryNonUTCIsLocalMidnight verifies that the daily AI
// budget window for a non-UTC workspace resets at the workspace's local
// midnight, not UTC midnight.
func TestDailyBudgetBoundaryNonUTCIsLocalMidnight(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}

	// 2026-07-18 09:00 JST. UTC-truncation would place the boundary at
	// 2026-07-18 00:00 UTC (== 09:00 JST), which is wrong: at 09:00 local
	// the day has only just begun in Tokyo.
	now := time.Date(2026, 7, 18, 9, 0, 0, 0, tokyo)

	got := DailyBudgetBoundary(now, "Asia/Tokyo")

	wantLocal := time.Date(2026, 7, 18, 0, 0, 0, 0, tokyo)
	if !got.Equal(wantLocal) {
		t.Fatalf("boundary = %s, want local midnight %s", got, wantLocal)
	}

	// The boundary is local midnight, so its wall-clock hour in Tokyo is 0.
	if h := got.In(tokyo).Hour(); h != 0 {
		t.Fatalf("boundary local hour = %d, want 0", h)
	}

	// Local midnight in Tokyo (UTC+9) is 15:00 UTC on the previous day.
	wantUTC := time.Date(2026, 7, 17, 15, 0, 0, 0, time.UTC)
	if !got.UTC().Equal(wantUTC) {
		t.Fatalf("boundary in UTC = %s, want %s", got.UTC(), wantUTC)
	}
}

// TestDailyBudgetBoundaryFallsBackToUTC verifies that an empty or invalid
// timezone yields a UTC-midnight boundary rather than an error.
func TestDailyBudgetBoundaryFallsBackToUTC(t *testing.T) {
	now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	for _, tz := range []string{"", "Not/AZone"} {
		got := DailyBudgetBoundary(now, tz)
		want := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("tz=%q boundary = %s, want %s", tz, got, want)
		}
	}
}

type fakeTZLoader struct {
	tz  string
	err error
}

func (f fakeTZLoader) FindWorkspaceTimezoneCountryById(_ context.Context, _ uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error) { //nolint:revive // method name mirrors the sqlc-generated Querier; renaming breaks interface satisfaction
	if f.err != nil {
		return generated.FindWorkspaceTimezoneCountryByIdRow{}, f.err
	}
	return generated.FindWorkspaceTimezoneCountryByIdRow{Timezone: f.tz}, nil
}

// TestWorkspaceDayStartUsesLoadedTimezone verifies the boundary is computed in
// the timezone reported by the loader, and that a load error degrades to UTC.
func TestWorkspaceDayStartUsesLoadedTimezone(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")

	got := WorkspaceDayStart(context.Background(), fakeTZLoader{tz: "Asia/Tokyo"}, 1)
	if h := got.In(tokyo).Hour(); h != 0 {
		t.Fatalf("loaded-tz boundary local hour = %d, want 0", h)
	}

	// A loader error must not panic or propagate: fall back to a UTC boundary.
	gotErr := WorkspaceDayStart(context.Background(), fakeTZLoader{err: errors.New("db down")}, 1)
	if h := gotErr.In(time.UTC).Hour(); h != 0 {
		t.Fatalf("error-fallback boundary UTC hour = %d, want 0", h)
	}
}
