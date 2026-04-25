package calendars

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/region"
)

// holidaySlug returns the system_slug used for a country's holiday feed.
// Caller must pass a valid ISO 3166-1 alpha-2 code.
func holidaySlug(country string) string {
	return "holidays." + strings.ToLower(country)
}

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
		DisplayColor: "#4285F4",
	})
	return err
}

// EnsureSystemCalendars creates the default holiday calendar for the
// workspace's country and auto-subscribes the requesting user as a viewer.
// When workspace.country is unset, no system calendar is created — users
// can explicitly subscribe later via the subscribe-system endpoint.
func EnsureSystemCalendars(ctx context.Context, q *generated.Queries, workspaceID, userID uint32) error {
	row, err := q.FindWorkspaceTimezoneCountryById(ctx, workspaceID)
	if err != nil {
		return err
	}
	if !row.Country.Valid || row.Country.String == "" {
		return nil // no country set, nothing to auto-subscribe
	}
	return SubscribeHolidayCalendar(ctx, q, workspaceID, userID, row.Country.String)
}

// SubscribeHolidayCalendar creates the system holiday calendar for the given
// country code (if it doesn't already exist) and subscribes the user as a
// viewer. Safe to call repeatedly; idempotent on (workspace, country).
func SubscribeHolidayCalendar(ctx context.Context, q *generated.Queries, workspaceID, userID uint32, country string) error {
	if err := region.ValidateCountry(country); err != nil {
		return err
	}
	slug := holidaySlug(country)
	existing, err := q.FindSystemCalendarBySlug(ctx, generated.FindSystemCalendarBySlugParams{
		WorkspaceID: workspaceID,
		SystemSlug:  sql.NullString{String: slug, Valid: true},
	})
	var calID uint32
	switch {
	case err == nil:
		calID = existing.ID
	case errors.Is(err, sql.ErrNoRows):
		calPublicID := types.New()
		// Store the English display name; localized rendering happens in the UI
		// via the holidays provider's displayName(locale) helper.
		displayName := region.SupportedCountries()[country]
		if displayName == "" {
			displayName = country
		}
		created, cerr := q.CreateCalendar(ctx, generated.CreateCalendarParams{
			PublicID:    calPublicID,
			WorkspaceID: workspaceID,
			Kind:        generated.CalendarsKindSystem,
			Name:        displayName + " Holidays",
			Color:       "#EA4335",
			SystemSlug:  sql.NullString{String: slug, Valid: true},
		})
		if cerr != nil {
			return cerr
		}
		calID = uint32(created)
	default:
		return err
	}

	// Subscribe user (idempotent — duplicate key is tolerated).
	subPublicID := types.New()
	_, err = q.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
		PublicID:     subPublicID,
		WorkspaceID:  workspaceID,
		CalendarID:   calID,
		UserID:       userID,
		DisplayColor: "#EA4335",
	})
	// Duplicate subscription is fine.
	if err != nil {
		var mysqlErr interface{ Number() uint16 }
		if errors.As(err, &mysqlErr) && mysqlErr.Number() == 1062 {
			return nil
		}
		// Also tolerate the generic "already exists" case — some drivers
		// wrap the error differently. The subscribe is additive; a second
		// call for the same user/calendar should be a no-op semantically.
		if strings.Contains(err.Error(), "Duplicate") {
			return nil
		}
		return err
	}
	return nil
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
