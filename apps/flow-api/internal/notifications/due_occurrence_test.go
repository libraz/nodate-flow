package notifications

import (
	"testing"
	"time"
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAllDayReminderIsDueAtZoneMidnightMinusOffset(t *testing.T) {
	t.Parallel()
	occurrence := utc("2026-11-22T00:00:00Z") // canonical key of 2026-11-22
	cases := []struct {
		name     string
		tz       string
		offset   int32
		now      string
		wantDue  bool
		wantKeyd string
	}{
		// Tokyo midnight is 2026-11-21T15:00Z; 60 minutes before is 14:00Z.
		{"before the reminder time", "Asia/Tokyo", 60, "2026-11-21T13:59:00Z", false, ""},
		{"at the reminder time", "Asia/Tokyo", 60, "2026-11-21T14:00:00Z", true, "2026-11-22T00:00:00Z"},
		{"after the day began", "Asia/Tokyo", 60, "2026-11-21T15:00:00Z", false, ""},
		// Los Angeles midnight is 2026-11-22T08:00Z, after the UTC midnight
		// the occurrence is keyed by; 300 minutes before is 03:00Z.
		{"west of UTC, past the UTC midnight", "America/Los_Angeles", 300, "2026-11-22T03:00:00Z", true, "2026-11-22T00:00:00Z"},
		{"west of UTC, before the reminder time", "America/Los_Angeles", 300, "2026-11-22T02:59:00Z", false, ""},
		{"west of UTC, after the day began", "America/Los_Angeles", 300, "2026-11-22T08:00:00Z", false, ""},
	}
	for _, c := range cases {
		base := pendingNotification{NotificationOffset: c.offset, AllDay: true, Timezone: c.tz}
		got, ok := dueOccurrence(base, occurrence, utc(c.now))
		if ok != c.wantDue {
			t.Errorf("%s: due = %v, want %v", c.name, ok, c.wantDue)
			continue
		}
		if ok && !got.OccurrenceStart.Equal(utc(c.wantKeyd)) {
			t.Errorf("%s: claim key = %v, want %v", c.name, got.OccurrenceStart, c.wantKeyd)
		}
	}
}

func TestTimedReminderIsUnchanged(t *testing.T) {
	t.Parallel()
	start := utc("2026-11-22T10:00:00Z")
	base := pendingNotification{NotificationOffset: 30, Timezone: "Asia/Tokyo"}
	if _, ok := dueOccurrence(base, start, utc("2026-11-22T09:29:00Z")); ok {
		t.Error("due before start - offset")
	}
	if _, ok := dueOccurrence(base, start, utc("2026-11-22T09:30:00Z")); !ok {
		t.Error("not due at start - offset")
	}
	if _, ok := dueOccurrence(base, start, utc("2026-11-22T10:00:00Z")); ok {
		t.Error("due once started")
	}
}

func TestMaxOffsetIsWithinTheScanHorizon(t *testing.T) {
	t.Parallel()
	const maxOffset = 10080
	if maxReminderOffsetMinutes < maxOffset {
		t.Fatalf("scan covers %d minutes, contract allows %d", maxReminderOffsetMinutes, maxOffset)
	}
	now := utc("2026-11-22T00:00:00Z")
	base := pendingNotification{NotificationOffset: maxOffset}
	start := now.Add(maxOffset * time.Minute)
	if _, ok := dueOccurrence(base, start, now); !ok {
		t.Error("a reminder with the largest offset is not due when it comes up")
	}
	if now.Add(reminderWindow + maxReminderOffsetMinutes*time.Minute).Before(start) {
		t.Error("the candidate scan ends before the occurrence")
	}
}

// A claim is keyed by the occurrence's canonical UTC midnight. In a zone
// west of UTC the reminder is due after that instant has passed, so a
// lookup that starts at now never sees the claim and sends again.
func TestClaimLookupReachesBackToTheOccurrenceKey(t *testing.T) {
	t.Parallel()
	now := utc("2026-11-22T03:00:00Z")
	due := []pendingNotification{{OccurrenceStart: utc("2026-11-22T00:00:00Z")}}
	if floor := claimFloor(due, now); floor.After(due[0].OccurrenceStart) {
		t.Errorf("claim lookup starts at %v, after the occurrence key %v", floor, due[0].OccurrenceStart)
	}
	if floor := claimFloor(nil, now); !floor.Equal(now) {
		t.Errorf("no due occurrence: floor = %v, want now", floor)
	}
}

func TestOverrideWithItsOwnAttendeeRowsDoesNotInheritTheSeries(t *testing.T) {
	t.Parallel()
	n := pendingNotification{ID: 20, SeriesID: 10}
	if got := attendeeSource(n, true); got != 20 {
		t.Errorf("override with rows reads %d, want its own (20)", got)
	}
	if got := attendeeSource(n, false); got != 10 {
		t.Errorf("override with none reads %d, want the series (10)", got)
	}
	if got := attendeeSource(pendingNotification{ID: 10}, false); got != 10 {
		t.Errorf("a plain row reads %d, want itself", got)
	}
}
