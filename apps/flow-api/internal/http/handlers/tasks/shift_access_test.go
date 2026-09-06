package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
)

// TestShiftRefusal covers every standing a caller can hold on the
// calendar an event lives on.
//
// A shift moves the event and the dates of the tasks linked to it, so it
// is a write to that calendar's contents: workspace membership is not the
// boundary, the calendar's own member list is. The role floor and the
// system-calendar refusal are the calendar handlers' rule, reached here
// rather than restated, so the two surfaces cannot drift apart.
func TestShiftRefusal(t *testing.T) {
	t.Parallel()

	member := func(kind calendar.CalendarsKind, role calendar.CalendarMembersRole) *calendarStanding {
		return &calendarStanding{kind: kind, role: role}
	}

	cases := []struct {
		name     string
		standing *calendarStanding
		want     *apierrors.Spec
	}{
		{
			name:     "no membership",
			standing: nil,
			want:     apierrors.CalendarEventNotFound,
		},
		{
			// Editor is the floor a write opens at, so editor is the grant
			// the caller is sent to ask for. Naming a stronger role sends
			// them after permission the operation never needed.
			name:     "viewer",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleViewer),
			want:     apierrors.CalendarCalendarEditorRoleRequired,
		},
		{
			name:     "editor",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleEditor),
			want:     nil,
		},
		{
			name:     "manager",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleManager),
			want:     nil,
		},
		{
			name:     "owner",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleOwner),
			want:     nil,
		},
		{
			name:     "system calendar at the strongest role",
			standing: member(calendar.CalendarsKindSystem, calendar.CalendarMembersRoleOwner),
			want:     apierrors.CalendarCalendarAccessDenied,
		},
		{
			name:     "system calendar below editor",
			standing: member(calendar.CalendarsKindSystem, calendar.CalendarMembersRoleViewer),
			want:     apierrors.CalendarCalendarEditorRoleRequired,
		},
		{
			// A role the enum does not carry outranks nothing. Reading an
			// unknown value as permission is how a role added to the schema
			// and not to the rule would arrive allowed.
			name:     "unrecognised role",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRole("auditor")),
			want:     apierrors.CalendarCalendarEditorRoleRequired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shiftRefusal(tc.standing)
			if tc.want == nil {
				assert.Nil(t, got, "the shift must be allowed")
				return
			}
			require.NotNil(t, got, "the shift must be refused")
			assert.Equal(t, tc.want.Code, got.Code)
		})
	}
}

// TestShiftRefusalHidesEventsOnUnreachableCalendars pins the disclosure
// half: an event on a calendar the caller holds no grant on answers
// exactly as a missing event does, code and status alike. Anything else
// turns the endpoint into an oracle for whether an id names a live event
// on a calendar its holder cannot see.
func TestShiftRefusalHidesEventsOnUnreachableCalendars(t *testing.T) {
	t.Parallel()

	got := shiftRefusal(nil)
	require.NotNil(t, got)
	assert.Equal(t, apierrors.CalendarEventNotFound.Code, got.Code,
		"a calendar the caller cannot reach must answer as a missing event")
	assert.Equal(t, apierrors.CalendarEventNotFound.Status, got.Status)

	// The two refusals a member can earn are distinguishable from that
	// one; a rule that answered not-found everywhere would satisfy the
	// assertion above while telling an editor nothing they can act on.
	viewer := shiftRefusal(&calendarStanding{
		kind: calendar.CalendarsKindPersonal,
		role: calendar.CalendarMembersRoleViewer,
	})
	require.NotNil(t, viewer)
	assert.NotEqual(t, apierrors.CalendarEventNotFound.Code, viewer.Code)
}

// TestShiftRefusalNamesTheRoleThatWouldAdmit closes the loop on the only
// part of a role refusal a caller can act on.
//
// Being told a grant is missing is useful only if the grant named is the
// one that would let the request through. A shift opens at editor, so a
// member below it who is sent to ask for owner is sent after permission
// this operation never required — and, on a calendar whose owner is
// somebody else, after permission they will not be given. The assertion
// is a round trip rather than a spelling check: the role the refusal
// names is granted, and the same request is then allowed.
func TestShiftRefusalNamesTheRoleThatWouldAdmit(t *testing.T) {
	t.Parallel()

	refusal := shiftRefusal(&calendarStanding{
		kind: calendar.CalendarsKindPersonal,
		role: calendar.CalendarMembersRoleViewer,
	})
	require.NotNil(t, refusal, "a viewer must be refused a shift")
	assert.Equal(t, apierrors.CalendarCalendarEditorRoleRequired.Code, refusal.Code,
		"a member below the write floor must be told editor is the grant that admits them")
	assert.NotEqual(t, apierrors.CalendarCalendarOwnerRoleRequired.Code, refusal.Code,
		"owner is not the floor a shift opens at, so naming it sends the caller after "+
			"permission this operation never needed")

	assert.Nil(t, shiftRefusal(&calendarStanding{
		kind: calendar.CalendarsKindPersonal,
		role: calendar.CalendarMembersRoleEditor,
	}), "the role the refusal named has to be the one that lets the shift through")
}

// TestShiftRefusalMatchesTheCalendarRule pins the shift routes to the
// answer the calendar package gives for the same standing, across every
// combination of kind and role.
//
// The routes reach that rule rather than restating it, and this is what
// keeps them reaching it. A local switch reinstated here would look
// correct on the day it was written and drift on the day the floor moved:
// the same calendar would answer one way through the calendar handlers
// and another through a shift, and a caller could tell which surface they
// had reached from the refusal alone.
func TestShiftRefusalMatchesTheCalendarRule(t *testing.T) {
	t.Parallel()

	kinds := []calendar.CalendarsKind{
		calendar.CalendarsKindPersonal,
		calendar.CalendarsKindSystem,
	}
	roles := []calendar.CalendarMembersRole{
		calendar.CalendarMembersRoleViewer,
		calendar.CalendarMembersRoleEditor,
		calendar.CalendarMembersRoleManager,
		calendar.CalendarMembersRoleOwner,
	}
	for _, kind := range kinds {
		for _, role := range roles {
			got := shiftRefusal(&calendarStanding{kind: kind, role: role})
			want := calendars.EventPathWriteRefusalSpec(&calendars.CalendarStanding{
				Kind: kind,
				Role: role,
			})
			if want == nil {
				assert.Nil(t, got, "%s/%s: the calendar rule allows the write", kind, role)
				continue
			}
			require.NotNil(t, got, "%s/%s: the calendar rule refuses the write", kind, role)
			assert.Equal(t, want.Code, got.Code, "%s/%s", kind, role)
			assert.Equal(t, want.Status, got.Status, "%s/%s", kind, role)
		}
	}

	// The no-membership case is the one the shift routes depend on most, so
	// it is asserted against the same rule rather than left to the sweep
	// above, which only walks standings a member can hold.
	got := shiftRefusal(nil)
	want := calendars.EventPathWriteRefusalSpec(nil)
	require.NotNil(t, got)
	require.NotNil(t, want)
	assert.Equal(t, want.Code, got.Code, "no membership")
	assert.Equal(t, want.Status, got.Status, "no membership")
}
