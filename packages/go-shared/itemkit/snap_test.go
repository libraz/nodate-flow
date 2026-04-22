package itemkit

import (
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/region"
)

// 2026-04-25 is a Saturday in UTC; 2026-04-27 is the following Monday.
var (
	sampleSaturday = time.Date(2026, 4, 25, 14, 30, 0, 0, time.UTC)
	sampleEndSat   = time.Date(2026, 4, 25, 15, 30, 0, 0, time.UTC)
	sampleMonday   = time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	sampleEndMon   = time.Date(2026, 4, 20, 11, 0, 0, 0, time.UTC)
)

func TestApplySnap_OffModeIsNoop(t *testing.T) {
	t.Parallel()
	out := applySnap(sampleSaturday, sampleEndSat, SnapConfig{Mode: region.SnapOff})
	if !out.NewStart.Equal(sampleSaturday) || !out.NewEnd.Equal(sampleEndSat) {
		t.Errorf("SnapOff should not modify times")
	}
	if out.NonWorkingDay || out.AutoSnapped {
		t.Errorf("SnapOff should not set any flags")
	}
}

func TestApplySnap_WorkingDayIsNoop(t *testing.T) {
	t.Parallel()
	out := applySnap(sampleMonday, sampleEndMon, SnapConfig{
		Mode:        region.SnapWarn,
		WorkingDays: "MTWTF__",
	})
	if !out.NewStart.Equal(sampleMonday) || !out.NewEnd.Equal(sampleEndMon) {
		t.Errorf("working day should not be modified")
	}
	if out.NonWorkingDay || out.AutoSnapped {
		t.Errorf("working day should not set any flags")
	}
}

func TestApplySnap_WarnBadgesNonWorkingDay(t *testing.T) {
	t.Parallel()
	out := applySnap(sampleSaturday, sampleEndSat, SnapConfig{
		Mode:        region.SnapWarn,
		WorkingDays: "MTWTF__",
	})
	if !out.NewStart.Equal(sampleSaturday) || !out.NewEnd.Equal(sampleEndSat) {
		t.Errorf("warn mode should preserve times")
	}
	if !out.NonWorkingDay {
		t.Error("warn mode should set NonWorkingDay for a Saturday")
	}
	if out.AutoSnapped {
		t.Error("warn mode should not auto-snap")
	}
}

func TestApplySnap_AutoForwardSnapsAcrossWeekend(t *testing.T) {
	t.Parallel()
	out := applySnap(sampleSaturday, sampleEndSat, SnapConfig{
		Mode:        region.SnapAuto,
		WorkingDays: "MTWTF__",
	})
	// Expected: shift by two days, preserve clock, preserve duration.
	wantStart := time.Date(2026, 4, 27, 14, 30, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 4, 27, 15, 30, 0, 0, time.UTC)
	if !out.NewStart.Equal(wantStart) {
		t.Errorf("NewStart: want %v, got %v", wantStart, out.NewStart)
	}
	if !out.NewEnd.Equal(wantEnd) {
		t.Errorf("NewEnd: want %v, got %v", wantEnd, out.NewEnd)
	}
	if !out.AutoSnapped {
		t.Error("AutoSnapped should be true")
	}
	if out.AutoSnappedFrom != "2026-04-25" {
		t.Errorf("from: want 2026-04-25, got %s", out.AutoSnappedFrom)
	}
	if out.AutoSnappedTo != "2026-04-27" {
		t.Errorf("to: want 2026-04-27, got %s", out.AutoSnappedTo)
	}
}

func TestApplySnap_AutoDegradesToWarnWhenAllOff(t *testing.T) {
	t.Parallel()
	out := applySnap(sampleSaturday, sampleEndSat, SnapConfig{
		Mode:        region.SnapAuto,
		WorkingDays: "_______",
	})
	if !out.NewStart.Equal(sampleSaturday) {
		t.Error("all-off should not shift times")
	}
	if out.AutoSnapped {
		t.Error("AutoSnapped should be false when NextWorkingDay refuses")
	}
	if !out.NonWorkingDay {
		t.Error("NonWorkingDay should be true as graceful degradation")
	}
}

func TestApplySnap_HolidayCountsWhenTreatEnabled(t *testing.T) {
	t.Parallel()
	holidays := map[string]struct{}{"2026-04-20": {}}
	out := applySnap(sampleMonday, sampleEndMon, SnapConfig{
		Mode:          region.SnapWarn,
		WorkingDays:   "MTWTF__",
		Holidays:      holidays,
		TreatHolidays: true,
	})
	if !out.NonWorkingDay {
		t.Error("Monday holiday with TreatHolidays=true should flag non-working")
	}
	// With TreatHolidays=false the same Monday is working again.
	out2 := applySnap(sampleMonday, sampleEndMon, SnapConfig{
		Mode:          region.SnapWarn,
		WorkingDays:   "MTWTF__",
		Holidays:      holidays,
		TreatHolidays: false,
	})
	if out2.NonWorkingDay {
		t.Error("Monday holiday with TreatHolidays=false should NOT flag non-working")
	}
}

func TestApplySnap_UndatedIsNoop(t *testing.T) {
	t.Parallel()
	var zero time.Time
	out := applySnap(zero, zero, SnapConfig{
		Mode:        region.SnapAuto,
		WorkingDays: "MTWTF__",
	})
	if !out.NewStart.IsZero() || !out.NewEnd.IsZero() {
		t.Error("undated event should stay undated")
	}
	if out.NonWorkingDay || out.AutoSnapped {
		t.Error("undated event should not carry flags")
	}
}

func TestFlagsForSnapOutcome_ClearsByDefault(t *testing.T) {
	t.Parallel()
	overlay := flagsForSnapOutcome(snapOutcome{})
	for _, k := range []string{
		FlagNonWorkingDay, FlagAutoSnapped,
		FlagAutoSnappedFrom, FlagAutoSnappedTo, FlagAutoSnappedReason,
	} {
		v, ok := overlay[k]
		if !ok {
			t.Errorf("clean outcome should list %s so stale flags clear", k)
			continue
		}
		if v != nil {
			t.Errorf("%s should be nil (clear) in a clean outcome, got %v", k, v)
		}
	}
}

func TestFlagsForSnapOutcome_AutoPopulatesDates(t *testing.T) {
	t.Parallel()
	overlay := flagsForSnapOutcome(snapOutcome{
		AutoSnapped:     true,
		AutoSnappedFrom: "2026-04-25",
		AutoSnappedTo:   "2026-04-27",
	})
	if overlay[FlagAutoSnapped] != true {
		t.Errorf("FlagAutoSnapped should be true, got %v", overlay[FlagAutoSnapped])
	}
	if overlay[FlagAutoSnappedFrom] != "2026-04-25" {
		t.Errorf("from wrong: %v", overlay[FlagAutoSnappedFrom])
	}
	if overlay[FlagAutoSnappedTo] != "2026-04-27" {
		t.Errorf("to wrong: %v", overlay[FlagAutoSnappedTo])
	}
}

func TestMergeFlags_PreservesUnknownKeys(t *testing.T) {
	t.Parallel()
	base := map[string]any{
		"custom_marker":   "keep_me",
		FlagNonWorkingDay: true,
	}
	overlay := map[string]any{
		FlagNonWorkingDay: nil, // clear
		FlagAutoSnapped:   true,
	}
	got := mergeFlags(base, overlay)
	if got["custom_marker"] != "keep_me" {
		t.Error("unknown key should be preserved")
	}
	if _, ok := got[FlagNonWorkingDay]; ok {
		t.Error("nil overlay should clear key")
	}
	if got[FlagAutoSnapped] != true {
		t.Error("overlay should add new key")
	}
}

func TestHasAnySnapFlag(t *testing.T) {
	t.Parallel()
	if hasAnySnapFlag(nil) {
		t.Error("nil should not have any snap flag")
	}
	if hasAnySnapFlag(map[string]any{"unrelated": true}) {
		t.Error("unrelated keys should not match")
	}
	if !hasAnySnapFlag(map[string]any{FlagNonWorkingDay: true}) {
		t.Error("FlagNonWorkingDay should match")
	}
	if !hasAnySnapFlag(map[string]any{FlagAutoSnapped: true}) {
		t.Error("FlagAutoSnapped should match")
	}
}
