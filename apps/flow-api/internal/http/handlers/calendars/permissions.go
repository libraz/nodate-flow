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
	errForbidden            = httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
	errInviteNotFound       = httpErr(apierrors.CalendarInviteNotFound)

	// errCalendarReadOnly refuses a write to a system calendar, whose rows
	// are owned by a provider feed rather than by anyone in the workspace.
	// It reuses the calendar access code because the refusal is the same
	// shape — this calendar is not yours to write to — and no member, at
	// any role, is the exception.
	errCalendarReadOnly = httpErr(apierrors.CalendarCalendarAccessDenied)
)

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
