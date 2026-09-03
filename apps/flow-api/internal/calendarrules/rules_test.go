package calendarrules

import (
	"database/sql"
	"testing"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func secs(v int64) *int64 { return &v }

// TestRequireEventChronology pins the three answers the rule has to give,
// because both transports now depend on all three: the refusal, the
// milestone, and the planning-stage event that carries no window at all.
func TestRequireEventChronology(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		start   *int64
		end     *int64
		refused bool
	}{
		{name: "ordered window is accepted", start: secs(100), end: secs(200)},
		{name: "zero-length window is a milestone", start: secs(100), end: secs(100)},
		{name: "inverted window is refused", start: secs(200), end: secs(100), refused: true},
		{name: "undated event carries no window", start: nil, end: nil},
		{name: "half a window is left to the pair rule", start: secs(200), end: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RequireEventChronology(tc.start, tc.end)
			if tc.refused {
				if err == nil {
					t.Fatal("the window was accepted; it would reach the database CHECK and come back unattributable")
				}
				if err.Spec != apierrors.CalendarEventEndBeforeStart {
					t.Errorf("refused with %s, want %s", err.Spec.Code, apierrors.CalendarEventEndBeforeStart.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("the window was refused with %s", err.Spec.Code)
			}
		})
	}
}

// TestRequireEventStartEndPair pins that a half window is refused and
// both ends absent is not, which is what makes a planning-stage event
// expressible.
func TestRequireEventStartEndPair(t *testing.T) {
	t.Parallel()

	if err := RequireEventStartEndPair(secs(100), nil); err == nil {
		t.Error("a start without an end was accepted")
	} else if err.Spec != apierrors.CalendarEventStartEndPairRequired {
		t.Errorf("refused with %s, want %s", err.Spec.Code, apierrors.CalendarEventStartEndPairRequired.Code)
	}
	if err := RequireEventStartEndPair(nil, secs(100)); err == nil {
		t.Error("an end without a start was accepted")
	}
	if err := RequireEventStartEndPair(nil, nil); err != nil {
		t.Errorf("an undated event was refused with %s", err.Spec.Code)
	}
	if err := RequireEventStartEndPair(secs(100), secs(200)); err != nil {
		t.Errorf("a complete window was refused with %s", err.Spec.Code)
	}
}

// TestRequireValidTimezone pins that the refusal names the member it is
// about, which is the whole reason the field is a parameter.
func TestRequireValidTimezone(t *testing.T) {
	t.Parallel()

	if err := RequireValidTimezone("timezone", "Asia/Tokyo"); err != nil {
		t.Fatalf("a known zone was refused with %s", err.Spec.Code)
	}
	err := RequireValidTimezone("timezone", "JST")
	if err == nil {
		t.Fatal("an abbreviation was accepted; it stores a zone no grid can resolve")
	}
	if err.Spec != apierrors.ValidationBodyFieldInvalid {
		t.Errorf("refused with %s, want %s", err.Spec.Code, apierrors.ValidationBodyFieldInvalid.Code)
	}
	if got := err.Details["field"]; got != "timezone" {
		t.Errorf("the refusal names field %v, want \"timezone\"", got)
	}
}

// TestNormalizeAllDayBounds pins the canonical form, and that a timed
// event is left alone — normalising one would move every meeting to
// midnight.
func TestNormalizeAllDayBounds(t *testing.T) {
	t.Parallel()

	start := sql.NullTime{Time: time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC), Valid: true}
	end := sql.NullTime{Time: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC), Valid: true}

	gotStart, gotEnd := NormalizeAllDayBounds(true, start, end)
	wantStart := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	if !gotStart.Time.Equal(wantStart) {
		t.Errorf("all-day start normalised to %s, want %s", gotStart.Time, wantStart)
	}
	if !gotEnd.Time.Equal(wantEnd) {
		t.Errorf("all-day end normalised to %s, want %s", gotEnd.Time, wantEnd)
	}

	sameStart, sameEnd := NormalizeAllDayBounds(false, start, end)
	if !sameStart.Time.Equal(start.Time) || !sameEnd.Time.Equal(end.Time) {
		t.Error("a timed event was truncated to midnight")
	}

	absent, _ := NormalizeAllDayBounds(true, sql.NullTime{}, sql.NullTime{})
	if absent.Valid {
		t.Error("an absent bound was given a value")
	}
}

// TestUnixSecondsTreatsTheZeroInstantAsAbsent pins the conversion the MCP
// tools depend on. An update that supplies one end of the window and
// finds nothing stored for the other holds the zero instant there;
// rendering it as year one would compare a missing value against a
// present one and refuse the wrong request.
func TestUnixSecondsTreatsTheZeroInstantAsAbsent(t *testing.T) {
	t.Parallel()

	if got := UnixSeconds(time.Time{}); got != nil {
		t.Errorf("the zero instant rendered as %d, want absent", *got)
	}
	at := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	got := UnixSeconds(at)
	if got == nil || *got != at.Unix() {
		t.Errorf("an instant rendered as %v, want %d", got, at.Unix())
	}
	if err := RequireEventChronology(UnixSeconds(at), UnixSeconds(time.Time{})); err != nil {
		t.Errorf("a window with one end unknown was refused with %s", err.Spec.Code)
	}
}
