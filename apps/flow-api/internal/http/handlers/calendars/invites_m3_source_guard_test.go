package calendars

import (
	"os"
	"strings"
	"testing"
)

func TestCalendarInviteM3SourceGuard(t *testing.T) {
	sourceBytes, err := os.ReadFile("invites.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, want := range []string{
		"handlerutil.IsDuplicateEntry(cerr)",
		"FindCalendarEventInviteForAttendee(ctx, calendar.FindCalendarEventInviteForAttendeeParams",
		"ReviveCalendarEventInvite(ctx, calendar.ReviveCalendarEventInviteParams",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("calendar invite create must converge on the one row per (event, attendee) by reviving it: missing %q", want)
		}
	}

	// The lookup has to see revoked rows. Filtering it to live ones
	// sends a re-invite into the insert path, where it collides with
	// the revoked row and fails permanently — the exact shape the
	// rename above was made to remove.
	if strings.Contains(source, "FindActiveCalendarEventInvite") {
		t.Fatal("calendar invite create must not look up invites filtered to enabled rows")
	}

	for _, want := range []string{
		"tx, err := deps.DB.BeginTx(ctx, nil)",
		"txCalendarQueries := calendar.New(tx)",
		"txCalendarQueries.MarkCalendarEventInviteAccepted(ctx, invite.ID)",
		"txCalendarQueries.UpdateAttendeeRsvp(ctx, calendar.UpdateAttendeeRsvpParams",
		"tx.Commit()",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("calendar invite accept must stamp accepted_at and RSVP in one transaction: missing %q", want)
		}
	}
}
