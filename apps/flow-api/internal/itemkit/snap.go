package itemkit

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// SnapConfig collects the per-actor inputs itemkit needs to honor the
// snap_to_working_day preference without itself reading users /
// workspaces / holiday-calendar rows. Callers resolve these once per
// request (typically in a handler) and pass the result to itemkit.
//
// The zero value (Mode == "" or SnapOff) disables all snap behavior.
type SnapConfig struct {
	// Mode controls what happens when a target day is non-working.
	//   - SnapOff / "": no effect; itemkit writes the requested time.
	//   - SnapWarn: write requested time, badge flags.non_working_day.
	//   - SnapAuto: forward-snap to the next working day, preserving
	//     time-of-day; badge flags.auto_snapped with from/to dates.
	Mode region.SnapMode

	// WorkingDays is the effective 7-char string (Mon..Sun). Empty
	// falls back to region.WorkingDaysDefault inside the resolver.
	WorkingDays string

	// Location is the timezone the target date is rendered in when
	// deciding which weekday it falls on. Nil means UTC.
	Location *time.Location

	// Holidays is the set of ISO-date (YYYY-MM-DD) strings that count as
	// public holidays for the actor. ResolveSnapConfig fills it from the
	// actor's effective country; empty means no holidays are considered
	// even when TreatHolidays is true.
	Holidays map[string]struct{}

	// TreatHolidays reflects users.treat_holidays_as_non_working. When
	// false, Holidays is ignored even if populated.
	TreatHolidays bool
}

// snapOutcome reports what snap logic did to a candidate (startAt,
// endAt) pair. It never mutates the inputs; callers apply the outcome
// themselves.
type snapOutcome struct {
	// NewStart / NewEnd are the (possibly unchanged) times to write.
	NewStart time.Time
	NewEnd   time.Time

	// NonWorkingDay is true when Mode == SnapWarn and the original
	// date fell on a non-working day. Indicates the caller should set
	// flags.non_working_day = true.
	NonWorkingDay bool

	// AutoSnapped is true when Mode == SnapAuto produced a date shift.
	// Carries the pre-snap and post-snap ISO dates for the flag payload.
	AutoSnapped     bool
	AutoSnappedFrom string
	AutoSnappedTo   string
}

// applySnap applies the configured snap policy to a candidate time
// range. Returns a snapOutcome without touching the DB. The caller
// persists the result and updates flags per outcome.
//
// When the configured mode is SnapOff / empty, or when the original
// start is zero (undated), the outcome mirrors the input unchanged.
func applySnap(startAt, endAt time.Time, cfg SnapConfig) snapOutcome {
	out := snapOutcome{NewStart: startAt, NewEnd: endAt}
	mode := cfg.Mode
	if mode == "" {
		mode = region.SnapOff
	}
	if mode == region.SnapOff {
		return out
	}
	if startAt.IsZero() {
		return out
	}

	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	if region.IsWorkingDay(cfg.WorkingDays, startAt, loc, cfg.Holidays, cfg.TreatHolidays) {
		return out
	}

	if mode == region.SnapWarn {
		out.NonWorkingDay = true
		return out
	}

	// SnapAuto: compute the next working day and shift the range by the
	// whole-day delta so the time-of-day and duration are preserved.
	next := region.NextWorkingDay(cfg.WorkingDays, startAt, loc, cfg.Holidays, cfg.TreatHolidays)
	if next.Equal(startAt) {
		// NextWorkingDay refused to move (all-off string) — degrade to
		// warn so the caller still gets a signal.
		out.NonWorkingDay = true
		return out
	}
	fromDate := startAt.In(loc).Format("2006-01-02")
	toDate := next.In(loc).Format("2006-01-02")
	dayDelta := dateOnly(next.In(loc)).Sub(dateOnly(startAt.In(loc)))
	out.NewStart = startAt.Add(dayDelta)
	if !endAt.IsZero() {
		out.NewEnd = endAt.Add(dayDelta)
	}
	out.AutoSnapped = true
	out.AutoSnappedFrom = fromDate
	out.AutoSnappedTo = toDate
	return out
}

// applySnapFlags persists the snap outcome's flag changes onto the
// given event. Snap-off / untouched outcomes still run so stale badges
// from an earlier run are cleared. Skipped entirely when the outcome
// reports no change AND the event has no flags to clean (cheap path).
func applySnapFlags(ctx context.Context, tx TX, eventID uint32, o snapOutcome) error {
	// Fast path: if nothing was touched AND no existing flags exist,
	// skip the read/write entirely.
	existing, err := loadEventFlags(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if !o.NonWorkingDay && !o.AutoSnapped {
		// Still need to clear stale snap flags if any are present.
		if !hasAnySnapFlag(existing) {
			return nil
		}
	}
	merged := mergeFlags(existing, flagsForSnapOutcome(o))
	return writeEventFlags(ctx, tx, eventID, merged)
}

// hasAnySnapFlag reports whether the flags map contains any of the
// snap-owned keys itemkit manages.
func hasAnySnapFlag(m map[string]any) bool {
	for _, k := range []string{
		FlagNonWorkingDay,
		FlagAutoSnapped,
		FlagAutoSnappedFrom,
		FlagAutoSnappedTo,
		FlagAutoSnappedReason,
	} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// flagsForSnapOutcome returns the flag overlay map that callers merge
// into the event's existing flags JSON. Overlay semantics: nil clears
// the key (see mergeFlags). A clean outcome returns nil-valued entries
// for both flags so stale badges are removed.
func flagsForSnapOutcome(o snapOutcome) map[string]any {
	overlay := map[string]any{
		FlagNonWorkingDay:     nil,
		FlagAutoSnapped:       nil,
		FlagAutoSnappedFrom:   nil,
		FlagAutoSnappedTo:     nil,
		FlagAutoSnappedReason: nil,
	}
	if o.NonWorkingDay {
		overlay[FlagNonWorkingDay] = true
	}
	if o.AutoSnapped {
		overlay[FlagAutoSnapped] = true
		overlay[FlagAutoSnappedFrom] = o.AutoSnappedFrom
		overlay[FlagAutoSnappedTo] = o.AutoSnappedTo
		overlay[FlagAutoSnappedReason] = "non_working_day"
	}
	return overlay
}
