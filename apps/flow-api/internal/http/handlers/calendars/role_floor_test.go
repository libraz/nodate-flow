package calendars

import (
	"net/http"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// A role refusal is only actionable if it names the grant that would have
// admitted the caller: they are already a member of the calendar, so the
// message is a request they will take to whoever administers it. One
// sentinel cannot carry that, because the floor is an argument at the call
// site — the resolvers ask for owner, manager and editor at different
// endpoints and each has to be told apart.
func TestRoleFloorSpecNamesTheRequestedFloor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		least    calendar.CalendarMembersRole
		wantCode string
	}{
		{
			name:     "deleting a calendar asks for owner",
			least:    calendar.CalendarMembersRoleOwner,
			wantCode: "CALENDAR.CALENDAR.OWNER_ROLE_REQUIRED",
		},
		{
			name:     "administering a calendar asks for manager",
			least:    calendar.CalendarMembersRoleManager,
			wantCode: "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED",
		},
		{
			name:     "changing a calendar's contents asks for editor",
			least:    calendar.CalendarMembersRoleEditor,
			wantCode: "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED",
		},
		{
			// No endpoint asks for a viewer floor: viewer is membership,
			// which resolveCalendar establishes before any floor applies.
			// It is covered so the case that names no useful grant has a
			// stated answer rather than whatever the switch falls through
			// to.
			name:     "a floor below editor names no grant that would help",
			least:    calendar.CalendarMembersRoleViewer,
			wantCode: "CALENDAR.CALENDAR.ACCESS_DENIED",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := roleFloorSpec(tc.least)
			if spec.Code != tc.wantCode {
				t.Errorf("floor %q: got code %q, want %q", tc.least, spec.Code, tc.wantCode)
			}
			if spec.Status != http.StatusForbidden {
				t.Errorf("floor %q: got status %d, want %d; a member who needs a higher role is inside the boundary, so the refusal is a 403 rather than a 404",
					tc.least, spec.Status, http.StatusForbidden)
			}

			// The sentinel the resolvers actually return has to carry the
			// same answer to the wire; a mapping that is right in
			// isolation and lost on the way out is not a fix.
			problem, ok := errRoleFloor(tc.least).(*handlerutil.ProblemDetails)
			if !ok {
				t.Fatalf("floor %q: errRoleFloor did not produce the problem envelope", tc.least)
			}
			if problem.Type != tc.wantCode {
				t.Errorf("floor %q: envelope carries code %q, want %q", tc.least, problem.Type, tc.wantCode)
			}
			if problem.Status != http.StatusForbidden {
				t.Errorf("floor %q: envelope carries status %d, want %d", tc.least, problem.Status, http.StatusForbidden)
			}
		})
	}
}

// The owner code exists for the one endpoint that genuinely requires an
// owner, and for nothing else. Reporting it from a lower floor was how
// every role refusal on a calendar came to tell the caller to find an
// owner when an editor or manager grant would have been enough.
func TestRoleFloorSpecReservesTheOwnerCodeForTheOwnerFloor(t *testing.T) {
	t.Parallel()

	for _, least := range []calendar.CalendarMembersRole{
		calendar.CalendarMembersRoleManager,
		calendar.CalendarMembersRoleEditor,
		calendar.CalendarMembersRoleViewer,
	} {
		if got := roleFloorSpec(least); got == apierrors.CalendarCalendarOwnerRoleRequired {
			t.Errorf("floor %q reports the owner code; only a floor of owner may", least)
		}
	}
}

// The write rule's floor and the code that reports it are one statement.
// A refusal naming a role other than the one the comparison used sends the
// caller to request a grant that would not change the answer.
func TestCalendarWriteRefusalNamesTheWriteFloor(t *testing.T) {
	t.Parallel()

	if calendarWriteFloor != calendar.CalendarMembersRoleEditor {
		t.Fatalf("the write floor is %q; the refusal below is written against editor", calendarWriteFloor)
	}

	cases := []struct {
		name     string
		decision CalendarWriteDecision
		wantCode string
		wantHTTP int
	}{
		{
			name:     "a member below editor is told which role writes",
			decision: CalendarWriteRoleTooLow,
			wantCode: "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED",
			wantHTTP: http.StatusForbidden,
		},
		{
			// Not a role refusal: the rows belong to a provider feed, so
			// no grant is the answer and naming one would be a false lead.
			name:     "a system calendar is refused without naming a role",
			decision: CalendarWriteCalendarReadOnly,
			wantCode: "CALENDAR.CALENDAR.ACCESS_DENIED",
			wantHTTP: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := CalendarWriteRefusalSpec(tc.decision)
			if spec == nil {
				t.Fatal("a refusal decision must produce a spec")
			}
			if spec.Code != tc.wantCode {
				t.Errorf("got code %q, want %q", spec.Code, tc.wantCode)
			}
			if spec.Status != tc.wantHTTP {
				t.Errorf("got status %d, want %d", spec.Status, tc.wantHTTP)
			}
		})
	}

	if spec := CalendarWriteRefusalSpec(CalendarWriteAllowed); spec != nil {
		t.Errorf("an allowed write must produce no refusal, got %q", spec.Code)
	}
}

// The event-id path answers a caller who never named a calendar, so its
// no-membership case is the one place the answer differs from the
// named-calendar path: telling that caller apart from an unknown event id
// would confirm the id is live on a calendar they cannot reach.
func TestEventPathWriteRefusalHidesTheCalendarItCannotShow(t *testing.T) {
	t.Parallel()

	spec := EventPathWriteRefusalSpec(nil)
	if spec.Code != "CALENDAR.EVENT.NOT_FOUND" {
		t.Errorf("no membership on the event path: got %q, want CALENDAR.EVENT.NOT_FOUND", spec.Code)
	}
	if spec.Status != http.StatusNotFound {
		t.Errorf("no membership on the event path: got status %d, want %d", spec.Status, http.StatusNotFound)
	}

	// A member is past that point, so the refusals stay the shared ones:
	// they can read the calendar, and a higher role is something they can
	// actually ask for.
	viewer := &CalendarStanding{Kind: calendar.CalendarsKindPersonal, Role: calendar.CalendarMembersRoleViewer}
	if got := EventPathWriteRefusalSpec(viewer).Code; got != "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED" {
		t.Errorf("a viewer on the event path: got %q, want CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED", got)
	}
	system := &CalendarStanding{Kind: calendar.CalendarsKindSystem, Role: calendar.CalendarMembersRoleOwner}
	if got := EventPathWriteRefusalSpec(system).Code; got != "CALENDAR.CALENDAR.ACCESS_DENIED" {
		t.Errorf("a system calendar on the event path: got %q, want CALENDAR.CALENDAR.ACCESS_DENIED", got)
	}
	editor := &CalendarStanding{Kind: calendar.CalendarsKindPersonal, Role: calendar.CalendarMembersRoleEditor}
	if spec := EventPathWriteRefusalSpec(editor); spec != nil {
		t.Errorf("an editor on a personal calendar must not be refused, got %q", spec.Code)
	}
}
