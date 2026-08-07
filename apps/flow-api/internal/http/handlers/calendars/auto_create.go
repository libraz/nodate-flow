package calendars

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// EnsurePersonalCalendar creates a personal calendar for the user in the
// workspace if one does not already exist. It is called lazily from
// ListCalendars so the calendar materialises on first access.
func EnsurePersonalCalendar(ctx context.Context, cq *calendar.Queries, workspaceID, userID uint32, displayName string) error {
	_, err := cq.FindPersonalCalendar(ctx, calendar.FindPersonalCalendarParams{
		WorkspaceID: workspaceID,
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true}, //#nosec G115 -- owner user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	calPublicID := types.New()
	calID, err := cq.CreateCalendar(ctx, calendar.CreateCalendarParams{
		PublicID:    calPublicID,
		WorkspaceID: workspaceID,
		Kind:        calendar.CalendarsKindPersonal,
		Name:        displayName,
		Color:       "#4285F4",
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true}, //#nosec G115 -- owner user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err != nil {
		return err
	}

	// The calendar is this user's own, so they own it. Access lives in
	// calendar_members; without this row the calendar would be created and
	// immediately unreachable.
	_, err = cq.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
		PublicID:    types.New(),
		WorkspaceID: workspaceID,
		CalendarID:  uint32(calID), //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		UserID:      userID,
		Role:        calendar.CalendarMembersRoleOwner,
		MemberColor: "#4285F4",
	})
	return err
}

// ensureDefaults creates the personal calendar lazily on the first
// ListCalendars call for a user in a workspace. Errors are logged but do
// not block the list response.
func ensureDefaults(ctx context.Context, q *generated.Queries, cq *calendar.Queries, workspaceID, userID uint32) {
	profile, err := q.FindUserProfileById(ctx, userID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load user profile for auto-create", "err", err)
		return
	}
	if err := EnsurePersonalCalendar(ctx, cq, workspaceID, userID, profile.DisplayName); err != nil {
		slog.WarnContext(ctx, "failed to auto-create personal calendar", "err", err)
	}
}
