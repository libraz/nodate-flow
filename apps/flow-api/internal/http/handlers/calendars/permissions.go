package calendars

import (
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// Sentinel errors returned by resolve/permission helpers. These are translated
// to Huma errors at the handler level.
var (
	errWorkspaceNotFound    = httpErr(apierrors.CalendarWorkspaceNotFound)
	errCalendarNotFound     = httpErr(apierrors.CalendarCalendarNotFound)
	errCalendarAccessDenied = httpErr(apierrors.CalendarCalendarAccessDenied)
	errAccessDenied         = httpErr(apierrors.CalendarCalendarAccessDenied)
	errEventNotFound        = httpErr(apierrors.CalendarEventNotFound)
	errForbidden            = httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
	errInviteNotFound       = httpErr(apierrors.CalendarInviteNotFound)
)

// canEditEvent reports whether the actor may change an event.
//
// Three ways to qualify: owning the event, being an attendee the owner
// marked can_edit, or holding manager or owner on the calendar. Workspace
// membership alone is not one of them — a calendar's audience is narrower
// than its workspace, and an editor on the calendar may add their own
// events without gaining the right to rewrite everyone else's.
//
// The manager path is the delegation case: on a shared calendar where each
// person has a layer, whoever coordinates the calendar has to be able to
// move other people's events without being made owner of each one.
func canEditEvent(
	actorUserID uint32,
	event calendar.FindCalendarEventByPublicIdRow,
	member calendar.FindCalendarMemberRow,
	attendee *calendar.FindCalendarEventAttendeeRow,
) bool {
	if event.OwnerUserID == actorUserID {
		return true
	}
	if attendee != nil && attendee.CanEdit {
		return true
	}
	return roleRank(member.Role) >= roleRank(calendar.CalendarMembersRoleManager)
}

// canSetOwner reports whether the actor may file an event under the given
// owner, which decides whose layer and colour it appears on.
//
// Anyone may create their own events. Filing one under somebody else is
// delegation and needs manager or owner on the calendar: the alternative is
// that any editor can put commitments on a colleague's layer.
func canSetOwner(
	actorUserID uint32,
	ownerUserID uint32,
	member calendar.FindCalendarMemberRow,
) bool {
	if actorUserID == ownerUserID {
		return true
	}
	return roleRank(member.Role) >= roleRank(calendar.CalendarMembersRoleManager)
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
