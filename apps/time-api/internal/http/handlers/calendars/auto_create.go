package calendars

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
)

// EnsurePersonalCalendar creates a personal calendar for the user in the
// workspace if one does not already exist. It is called lazily from
// ListCalendars so the calendar materialises on first access.
func EnsurePersonalCalendar(ctx context.Context, q *generated.Queries, workspaceID, userID uint32, displayName string) error {
	_, err := q.FindPersonalCalendar(ctx, generated.FindPersonalCalendarParams{
		WorkspaceID: workspaceID,
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true},
	})
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	calPublicID := types.New()
	calID, err := q.CreateCalendar(ctx, generated.CreateCalendarParams{
		PublicID:    calPublicID,
		WorkspaceID: workspaceID,
		Kind:        generated.CalendarsKindPersonal,
		Name:        displayName,
		Color:       "#4285F4",
		OwnerUserID: sql.NullInt32{Int32: int32(userID), Valid: true},
	})
	if err != nil {
		return err
	}

	subPublicID := types.New()
	_, err = q.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
		PublicID:     subPublicID,
		WorkspaceID:  workspaceID,
		CalendarID:   uint32(calID),
		UserID:       userID,
		Role:         generated.CalendarSubscriptionsRoleOwner,
		MemberColor:  "#4285F4",
		DisplayColor: "#4285F4",
	})
	return err
}

// EnsureSystemCalendars creates the default system calendars (e.g. Japanese
// holidays) for the workspace if they do not already exist. The requesting
// user is auto-subscribed as a viewer.
func EnsureSystemCalendars(ctx context.Context, q *generated.Queries, workspaceID, userID uint32) error {
	slug := "holidays.jp"
	_, err := q.FindSystemCalendarBySlug(ctx, generated.FindSystemCalendarBySlugParams{
		WorkspaceID: workspaceID,
		SystemSlug:  sql.NullString{String: slug, Valid: true},
	})
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	calPublicID := types.New()
	calID, err := q.CreateCalendar(ctx, generated.CreateCalendarParams{
		PublicID:    calPublicID,
		WorkspaceID: workspaceID,
		Kind:        generated.CalendarsKindSystem,
		Name:        "日本の祝日",
		Color:       "#EA4335",
		SystemSlug:  sql.NullString{String: slug, Valid: true},
	})
	if err != nil {
		return err
	}

	subPublicID := types.New()
	_, err = q.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
		PublicID:     subPublicID,
		WorkspaceID:  workspaceID,
		CalendarID:   uint32(calID),
		UserID:       userID,
		Role:         generated.CalendarSubscriptionsRoleViewer,
		MemberColor:  "#EA4335",
		DisplayColor: "#EA4335",
	})
	return err
}

// ensureDefaults creates personal and system calendars lazily on the first
// ListCalendars call for a user in a workspace. Errors are logged but do
// not block the list response.
func ensureDefaults(ctx context.Context, q *generated.Queries, workspaceID, userID uint32) {
	profile, err := q.FindUserProfileById(ctx, userID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load user profile for auto-create", "err", err)
		return
	}
	if err := EnsurePersonalCalendar(ctx, q, workspaceID, userID, profile.DisplayName); err != nil {
		slog.WarnContext(ctx, "failed to auto-create personal calendar", "err", err)
	}
	if err := EnsureSystemCalendars(ctx, q, workspaceID, userID); err != nil {
		slog.WarnContext(ctx, "failed to auto-create system calendars", "err", err)
	}
}
