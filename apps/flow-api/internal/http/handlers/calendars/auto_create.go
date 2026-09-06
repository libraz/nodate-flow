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

// defaultPersonalCalendarColor is the colour a personal calendar and its
// owner grant are created with.
const defaultPersonalCalendarColor = "#4285F4"

// EnsurePersonalCalendar makes the user's personal calendar in the
// workspace both exist and be reachable: the calendars row, and the
// calendar_members row that access is actually read from. It is called
// lazily from ListCalendars so both materialise on first access.
//
// Existence and reachability are ensured separately because they are
// written separately — a calendar can exist with no grant on it, and
// then no creation path will ever run for that user again, so this is
// the only place the grant can still appear.
func EnsurePersonalCalendar(ctx context.Context, cq *calendar.Queries, workspaceID, userID uint32, displayName string) error {
	existing, err := cq.FindPersonalCalendar(ctx, calendar.FindPersonalCalendarParams{
		WorkspaceID: workspaceID,
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true}, //#nosec G115 -- owner user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err == nil {
		return ensureOwnerMembership(ctx, cq, workspaceID, existing.ID, userID, existing.Color)
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
		Color:       defaultPersonalCalendarColor,
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true}, //#nosec G115 -- owner user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err != nil {
		return err
	}

	return ensureOwnerMembership(
		ctx, cq, workspaceID,
		uint32(calID), //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		userID, defaultPersonalCalendarColor,
	)
}

// ensureOwnerMembership gives the user the owner grant on the personal
// calendar they own. The calendar is this user's own, so they own it.
// Access lives in calendar_members; without this row the calendar is
// unreachable, whether it was just created or has been sitting there
// since the workspace was.
//
// The existing grant is looked up first rather than upserted blindly:
// UpsertCalendarMember is safe to call for a pair that already has a row
// (ON DUPLICATE KEY UPDATE, no error), but it overwrites role,
// member_color and invited_by_user_id from the values passed, so calling
// it on every list would reset a colour or a role assigned since.
//
// A grant that exists but was revoked is re-granted, because the
// calendars row still names this user as the owner — the two rows
// disagreeing is the unreachable state this repairs.
func ensureOwnerMembership(ctx context.Context, cq *calendar.Queries, workspaceID, calendarID, userID uint32, color string) error {
	_, err := cq.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
		CalendarID: calendarID,
		UserID:     userID,
	})
	if err == nil {
		return nil // already reachable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = cq.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
		PublicID:    types.New(),
		WorkspaceID: workspaceID,
		CalendarID:  calendarID,
		UserID:      userID,
		Role:        calendar.CalendarMembersRoleOwner,
		MemberColor: color,
	})
	return err
}

// ensureDefaults materialises the personal calendar and the grant that
// reaches it lazily, on the first ListCalendars call for a user in a
// workspace. Errors are logged but do not block the list response.
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
