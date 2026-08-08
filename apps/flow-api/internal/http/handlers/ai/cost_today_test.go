package ai

import (
	"testing"
	"time"
)

// TestCostTodayBodyReportsTheEnforcedWindow pins that the meter describes
// the window it measured rather than a day computed somewhere else.
//
// The Tokyo case is the one that used to be wrong: local midnight is 15:00
// UTC the previous day, so formatting the instant in UTC — or in whatever
// zone the browser reported — names a different date than the one the
// budget actually reset on.
func TestCostTodayBodyReportsTheEnforcedWindow(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	windowStart := time.Date(2026, 7, 18, 0, 0, 0, 0, tokyo)

	body := costTodayBody(1234, windowStart)

	if body.Date != "2026-07-18" {
		t.Errorf("date = %q, want the workspace's own day 2026-07-18 (the same instant is 2026-07-17 in UTC)", body.Date)
	}
	if body.WindowStartsAt != windowStart.Unix() {
		t.Errorf("windowStartsAt = %d, want %d", body.WindowStartsAt, windowStart.Unix())
	}
	if body.CostUsd != 12.34 {
		t.Errorf("costUsd = %v, want 12.34", body.CostUsd)
	}
}

// TestCostTodayBodyZeroSpend covers the empty workspace: no spend still has
// a window, and reporting one without the other is what left support
// unable to tell "nothing was spent" from "nothing was measured".
func TestCostTodayBodyZeroSpend(t *testing.T) {
	t.Parallel()

	windowStart := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	body := costTodayBody(0, windowStart)

	if body.CostUsd != 0 {
		t.Errorf("costUsd = %v, want 0", body.CostUsd)
	}
	if body.WindowStartsAt != windowStart.Unix() {
		t.Errorf("windowStartsAt = %d, want %d", body.WindowStartsAt, windowStart.Unix())
	}
}
