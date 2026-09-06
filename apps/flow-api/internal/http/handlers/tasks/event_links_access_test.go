package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// TestEventVisibilityRefusal covers every standing a caller can hold on
// the calendar an event lives on.
//
// The floor is membership itself. Reading an event's link set is reading
// the calendar's contents, which every member of a calendar may do, so
// the role ordering the write rule turns on has no say here — and
// neither does the calendar's kind, since a provider feed is there to be
// read.
func TestEventVisibilityRefusal(t *testing.T) {
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
			name:     "viewer",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleViewer),
			want:     nil,
		},
		{
			name:     "editor",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleEditor),
			want:     nil,
		},
		{
			name:     "owner",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRoleOwner),
			want:     nil,
		},
		{
			// A provider feed cannot be written, which is why the write
			// rule refuses one at every role. Subscribing to it is what a
			// system calendar is for, so the same refusal on a read would
			// deny its ordinary use.
			name:     "system calendar at the weakest role",
			standing: member(calendar.CalendarsKindSystem, calendar.CalendarMembersRoleViewer),
			want:     nil,
		},
		{
			// A role the enum does not carry is still a live membership
			// row, and membership is the whole floor.
			name:     "unrecognised role",
			standing: member(calendar.CalendarsKindPersonal, calendar.CalendarMembersRole("auditor")),
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := eventVisibilityRefusal(tc.standing)
			if tc.want == nil {
				assert.Nil(t, got, "the read must be allowed")
				return
			}
			require.NotNil(t, got, "the read must be refused")
			assert.Equal(t, tc.want.Code, got.Code)
		})
	}
}

// TestEventVisibilityRefusalHidesEventsOnUnreachableCalendars pins the
// disclosure half: an event on a calendar the caller holds no grant on
// answers exactly as a missing event does, code and status alike.
// Anything else turns the endpoint into an oracle for whether an id
// names a live event on a calendar its holder cannot see.
func TestEventVisibilityRefusalHidesEventsOnUnreachableCalendars(t *testing.T) {
	t.Parallel()

	got := eventVisibilityRefusal(nil)
	require.NotNil(t, got)
	assert.Equal(t, apierrors.CalendarEventNotFound.Code, got.Code,
		"a calendar the caller cannot reach must answer as a missing event")
	assert.Equal(t, apierrors.CalendarEventNotFound.Status, got.Status)
}

// TestEventVisibilityFloorIsBelowTheShiftFloor states the relationship
// between the two rules rather than restating either: every standing the
// write side lets through is one the read side lets through, and the
// read side additionally admits members the write side refuses.
//
// Written as a comparison so the pair cannot drift into agreement. A
// read floor that quietly rose to the write floor would still satisfy
// every case above one by one; what it would break is this — a viewer
// who may open the calendar losing the ability to see what is on it.
func TestEventVisibilityFloorIsBelowTheShiftFloor(t *testing.T) {
	t.Parallel()

	standings := []*calendarStanding{
		nil,
		{kind: calendar.CalendarsKindPersonal, role: calendar.CalendarMembersRoleViewer},
		{kind: calendar.CalendarsKindPersonal, role: calendar.CalendarMembersRoleEditor},
		{kind: calendar.CalendarsKindPersonal, role: calendar.CalendarMembersRoleManager},
		{kind: calendar.CalendarsKindPersonal, role: calendar.CalendarMembersRoleOwner},
		{kind: calendar.CalendarsKindSystem, role: calendar.CalendarMembersRoleOwner},
	}

	looser := 0
	for _, standing := range standings {
		mayShift := shiftRefusal(standing) == nil
		mayRead := eventVisibilityRefusal(standing) == nil
		if mayShift {
			assert.True(t, mayRead, "a standing that may shift an event must be able to read it")
		}
		if mayRead && !mayShift {
			looser++
		}
	}
	assert.Positive(t, looser,
		"the read floor must admit standings the write floor refuses")
}
