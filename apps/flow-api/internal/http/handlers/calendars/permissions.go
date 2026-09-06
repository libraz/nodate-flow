package calendars

import (
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// Sentinel errors returned by resolve/permission helpers. These are translated
// to Huma errors at the handler level.
var (
	errWorkspaceNotFound    = httpErr(apierrors.CalendarWorkspaceNotFound)
	errCalendarNotFound     = httpErr(apierrors.CalendarCalendarNotFound)
	errCalendarAccessDenied = httpErr(apierrors.CalendarCalendarAccessDenied)
	errAccessDenied         = httpErr(apierrors.CalendarCalendarAccessDenied)
	errEventNotFound        = httpErr(apierrors.CalendarEventNotFound)
	errInviteNotFound       = httpErr(apierrors.CalendarInviteNotFound)
)

// roleFloorSpec answers which refusal a caller earns for holding a
// calendar_members role below the floor an operation requires.
//
// The code names the role that would let the request through, because
// that is the only part of the refusal the caller can act on: they are
// already a member of the calendar, so what they lack is a grant, and a
// code naming a role the operation does not require sends them to ask for
// the wrong one. The floor is an argument at the call site rather than a
// property of the package, so a single sentinel cannot carry it — the
// code has to follow the floor that was asked for.
//
// Below editor there is no role worth naming. A floor of viewer is
// membership itself, which resolveCalendar has already established, so
// reaching here under one means the stored role is not a value this build
// ranks; it fails closed with the same refusal a non-member gets rather
// than pointing at a grant that would not help.
func roleFloorSpec(least calendar.CalendarMembersRole) *apierrors.Spec {
	switch least {
	case calendar.CalendarMembersRoleOwner:
		return apierrors.CalendarCalendarOwnerRoleRequired
	case calendar.CalendarMembersRoleManager:
		return apierrors.CalendarCalendarManagerRoleRequired
	case calendar.CalendarMembersRoleEditor:
		return apierrors.CalendarCalendarEditorRoleRequired
	case calendar.CalendarMembersRoleViewer:
		return apierrors.CalendarCalendarAccessDenied
	}
	return apierrors.CalendarCalendarAccessDenied
}

// errRoleFloor is [roleFloorSpec] as the sentinel the resolvers return.
func errRoleFloor(least calendar.CalendarMembersRole) error {
	return httpErr(roleFloorSpec(least))
}

// canEditEvent and canSetOwner adapt this package's sqlc row types to
// the shared rule in eventacl. The rule itself lives there because MCP
// answers the same question about the same rows and used to answer it
// differently — keyed on calendars.owner_user_id, which is NULL on every
// shared calendar, so a manager could move an event in the web app and
// never through an agent.
func canEditEvent(
	actorUserID uint32,
	event calendar.FindCalendarEventByPublicIdRow,
	member calendar.FindCalendarMemberRow,
	attendee *calendar.FindCalendarEventAttendeeRow,
) bool {
	return eventacl.CanEdit(event.OwnerUserID, eventacl.Editor{
		UserID:          actorUserID,
		CalendarRole:    eventacl.Role(member.Role),
		AttendeeCanEdit: attendee != nil && attendee.CanEdit,
	})
}

func canSetOwner(
	actorUserID uint32,
	ownerUserID uint32,
	member calendar.FindCalendarMemberRow,
) bool {
	return eventacl.CanSetOwner(ownerUserID, eventacl.Editor{
		UserID:       actorUserID,
		CalendarRole: eventacl.Role(member.Role),
	})
}

// validateInvite checks that the invite has not expired and has not exceeded its use limit.
func validateInvite(expiresAt sql.NullTime, maxUses sql.NullInt32, useCount uint32) error {
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return errInviteNotFound
	}
	if maxUses.Valid && useCount >= uint32(maxUses.Int32) { //#nosec G115 -- max_uses is INT NOT NULL non-negative, fits uint32
		return errInviteNotFound
	}
	return nil
}
