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
		"FindActiveCalendarEventInvite(ctx, calendar.FindActiveCalendarEventInviteParams",
		"RotateCalendarEventInviteToken(ctx, calendar.RotateCalendarEventInviteTokenParams",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("calendar invite create must converge duplicate insert races by rotating the existing invite: missing %q", want)
		}
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
