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
	errForbidden            = httpErr(apierrors.CalendarCalendarManagerRoleRequired)
	errInviteNotFound       = httpErr(apierrors.CalendarInviteNotFound)
)

// canEditEvent checks if the actor can modify an event.
//
// post-R5.1: calendar_subscriptions.role was dropped; ws members have edit
// access. event-level visibility plus the event owner / can_edit attendee
// relationship is the real ACL (rebuilt properly in R5.2 via itemkit).
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
	return true
}

// canSetOwner checks if the actor can create events on behalf of another user.
//
// post-R5.1: ws members have edit access; rebuilt properly in R5.2.
func canSetOwner(
	actorUserID uint32,
	ownerUserID uint32,
	_ generated.FindCalendarSubscriptionRow,
) bool {
	if actorUserID == ownerUserID {
		return true
	}
	return true
}

// isOwnerOrManager previously checked the subscription role.
//
// post-R5.1: ws members have edit access; event-level visibility is the real
// ACL (applied later). Always returns true; rebuilt properly in R5.2.
func isOwnerOrManager(_ generated.FindCalendarSubscriptionRow) bool {
	return true
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
