package calendars

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
)

// Sentinel errors returned by resolve/permission helpers. These are translated
// to Huma errors at the handler level.
var (
	errWorkspaceNotFound   = huma.Error404NotFound("Workspace not found")
	errCalendarNotFound    = huma.Error404NotFound("Calendar not found")
	errCalendarAccessDenied = huma.Error403Forbidden("You do not have access to this calendar")
	errAccessDenied        = huma.Error403Forbidden("Access denied")
	errEventNotFound       = huma.Error404NotFound("Event not found")
	errForbidden           = huma.Error403Forbidden("You do not have permission to perform this action")
	errInviteNotFound      = huma.Error404NotFound("Invite not found or expired")
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
