package mcp

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
)

// specOf pulls the catalogue spec back out of a refusal so two transports
// can be compared by the answer a client sees rather than by the code that
// produced it.
func specOf(t *testing.T, err error) *apierrors.Spec {
	t.Helper()
	if err == nil {
		return nil
	}
	api, ok := err.(*apierrors.APIError)
	if !ok {
		t.Fatalf("refusal is not an APIError: %T (%v)", err, err)
	}
	return api.Spec
}

// A caller must not be able to tell which transport they reached from the
// refusal alone. This transport therefore takes both halves of the write
// rule from the calendars package — the decision and the code it is
// reported as — instead of naming codes of its own, which is how it came
// to answer "only calendar owners" to a member who needed editor.
func TestMCPCalendarWriteRefusalMatchesREST(t *testing.T) {
	t.Parallel()

	kinds := []calendar.CalendarsKind{
		calendar.CalendarsKindPersonal,
		calendar.CalendarsKindSystem,
	}
	roles := []calendar.CalendarMembersRole{
		calendar.CalendarMembersRoleOwner,
		calendar.CalendarMembersRoleManager,
		calendar.CalendarMembersRoleEditor,
		calendar.CalendarMembersRoleViewer,
		// A role this build does not rank, which is what an unranked
		// stored value looks like to the comparison.
		calendar.CalendarMembersRole(""),
	}

	for _, kind := range kinds {
		for _, role := range roles {
			want := calendars.CalendarWriteRefusalSpec(calendars.DecideCalendarWrite(kind, role))
			got := specOf(t, checkCalendarWrite(kind, role))
			if got != want {
				t.Errorf("%s calendar, role %q: MCP answers %s, REST answers %s",
					kind, role, specCode(got), specCode(want))
			}
		}
	}
}

// specCode renders a spec for a failure message, including the allowed
// case, so a divergence reads as two answers rather than a nil pointer.
func specCode(spec *apierrors.Spec) string {
	if spec == nil {
		return "<allowed>"
	}
	return spec.Code
}

// The role codes a client can actually receive from this transport.
//
// The write floor is editor, so no MCP path can answer the manager or
// owner code: there is no calendar-administration tool, and a floor above
// editor has nothing here to be applied by. That is the parity statement
// for the two higher floors — REST answers them on endpoints that change
// the calendar itself or its membership, and MCP offers no such endpoint
// to disagree on. If one is ever added it must take its refusal from the
// calendars package too, and this test will start failing until it does.
func TestMCPCalendarPathsApplyNoFloorAboveEditor(t *testing.T) {
	t.Parallel()

	for _, role := range []calendar.CalendarMembersRole{
		calendar.CalendarMembersRoleEditor,
		calendar.CalendarMembersRoleManager,
		calendar.CalendarMembersRoleOwner,
	} {
		if err := checkCalendarWrite(calendar.CalendarsKindPersonal, role); err != nil {
			t.Errorf("role %q is refused a calendar write: %v; the write floor is editor, so every role at or above it is admitted",
				role, err)
		}
	}

	for _, kind := range []calendar.CalendarsKind{
		calendar.CalendarsKindPersonal,
		calendar.CalendarsKindSystem,
	} {
		for _, role := range []calendar.CalendarMembersRole{
			calendar.CalendarMembersRoleOwner,
			calendar.CalendarMembersRoleManager,
			calendar.CalendarMembersRoleEditor,
			calendar.CalendarMembersRoleViewer,
		} {
			switch specOf(t, checkCalendarWrite(kind, role)) {
			case apierrors.CalendarCalendarManagerRoleRequired, apierrors.CalendarCalendarOwnerRoleRequired:
				t.Errorf("%s calendar, role %q: MCP answers a floor it does not apply", kind, role)
			}
		}
	}
}

// The event tools take an event id and never a calendar id, so their
// no-membership refusal has to be the one the calendars package reserves
// for that shape: distinguishing it from an unknown event id would confirm
// that the id names a live event on a calendar the caller cannot open,
// list or read.
//
// The tools that do name a calendar keep answering access-denied, and that
// difference is deliberate rather than drift: naming the calendar is what
// makes the answer something the caller could already have obtained.
func TestMCPEventPathRefusalHidesWhatItCannotShow(t *testing.T) {
	t.Parallel()

	if got := calendars.EventPathWriteRefusalSpec(nil); got != apierrors.CalendarEventNotFound {
		t.Errorf("the event path answers %s for a caller with no membership; it must answer %s",
			specCode(got), apierrors.CalendarEventNotFound.Code)
	}

	// The named-calendar gate is authorizeCalendar's, and it stays
	// access-denied. The two answers are meant to differ: collapsing them
	// would either hide a calendar from someone who named it and can be
	// told plainly that it is not theirs, or turn the event path back into
	// an oracle. Both transports draw the line at the same place — whether
	// the request named a calendar — not at which transport it arrived on.
	if calendars.EventPathWriteRefusalSpec(nil) == apierrors.CalendarCalendarAccessDenied {
		t.Error("the event path now answers the named-calendar refusal; a request that never named a calendar must not be told whether the event exists")
	}
}
