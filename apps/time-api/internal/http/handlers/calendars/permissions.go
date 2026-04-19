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
func canEditEvent(
	actorUserID uint32,
	event generated.FindCalendarEventByPublicIdRow,
	sub generated.FindCalendarSubscriptionRow,
	attendee *generated.FindCalendarEventAttendeeRow,
) bool {
	if event.OwnerUserID == actorUserID {
		return true
	}
	if attendee != nil && attendee.CanEdit {
		return true
	}
	if sub.Role == generated.CalendarSubscriptionsRoleOwner ||
		sub.Role == generated.CalendarSubscriptionsRoleManager {
		return true
	}
	return false
}

// canSetOwner checks if the actor can create events on behalf of another user.
func canSetOwner(
	actorUserID uint32,
	ownerUserID uint32,
	sub generated.FindCalendarSubscriptionRow,
) bool {
	if actorUserID == ownerUserID {
		return true
	}
	return sub.Role == generated.CalendarSubscriptionsRoleOwner ||
		sub.Role == generated.CalendarSubscriptionsRoleManager
}

// isOwnerOrManager returns true if the subscription role is owner or manager.
func isOwnerOrManager(sub generated.FindCalendarSubscriptionRow) bool {
	return sub.Role == generated.CalendarSubscriptionsRoleOwner ||
		sub.Role == generated.CalendarSubscriptionsRoleManager
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
