package calendars

import (
	"database/sql"
	"time"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
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

// canEditEvent returns true iff the actor is the event owner or an attendee
// with can_edit. Workspace membership alone does not grant edit access;
// event-level owner/attendee relationship is the edit gate.
func canEditEvent(
	actorUserID uint32,
	event generated.FindCalendarEventByPublicIdRow,
	_ generated.FindCalendarSubscriptionRow,
	attendee *generated.FindCalendarEventAttendeeRow,
) bool {
	if event.OwnerUserID == actorUserID {
		return true
	}
	if attendee != nil && attendee.CanEdit {
		return true
	}
	return false
}

// canSetOwner returns true iff the actor sets themselves as the event owner.
// Setting another user as owner (manager-style delegation) is not supported
// in the current post-itemkit model — rebuilt later when manager role is
// reintroduced.
func canSetOwner(
	actorUserID uint32,
	ownerUserID uint32,
	_ generated.FindCalendarSubscriptionRow,
) bool {
	if actorUserID == ownerUserID {
		return true
	}
	return false
}

// validateInvite checks that the invite has not expired and has not exceeded its use limit.
func validateInvite(expiresAt sql.NullTime, maxUses sql.NullInt32, useCount uint32) error {
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return errInviteNotFound
	}
	if maxUses.Valid && useCount >= uint32(maxUses.Int32) {
		return errInviteNotFound
	}
	return nil
}
