package calendars

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
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

// EnsureSystemCalendars creates the default holiday calendar for the
// workspace's country and auto-subscribes the requesting user as a viewer.
// When workspace.country is unset, no system calendar is created — users
// can explicitly subscribe later via the subscribe-system endpoint.
func EnsureSystemCalendars(ctx context.Context, q *generated.Queries, cq *calendar.Queries, workspaceID, userID uint32) error {
	row, err := q.FindWorkspaceTimezoneCountryById(ctx, workspaceID)
	if err != nil {
		return err
	}
	if !row.Country.Valid || row.Country.String == "" {
		return nil // no country set, nothing to auto-subscribe
	}
	return SubscribeHolidayCalendar(ctx, cq, workspaceID, userID, row.Country.String)
}

// SubscribeHolidayCalendar creates the system holiday calendar for the given
// country code (if it doesn't already exist) and subscribes the user as a
// viewer. Safe to call repeatedly; idempotent on (workspace, country).
func SubscribeHolidayCalendar(ctx context.Context, cq *calendar.Queries, workspaceID, userID uint32, country string) error {
	if err := region.ValidateCountry(country); err != nil {
		return err
	}
	slug := holidaySlug(country)
	existing, err := cq.FindSystemCalendarBySlug(ctx, calendar.FindSystemCalendarBySlugParams{
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
		created, cerr := cq.CreateCalendar(ctx, calendar.CreateCalendarParams{
			PublicID:    calPublicID,
			WorkspaceID: workspaceID,
			Kind:        calendar.CalendarsKindSystem,
			Name:        displayName + " Holidays",
			Color:       "#EA4335",
			SystemSlug:  sql.NullString{String: slug, Valid: true},
		})
		if cerr != nil {
			return cerr
		}
		calID = uint32(created) //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	default:
		return err
	}

	// Grant read access to the shared system calendar. Viewer, not owner:
	// its contents come from the holiday provider, and nobody edits it.
	// The upsert makes a repeat call a no-op.
	_, err = cq.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
		PublicID:    types.New(),
		WorkspaceID: workspaceID,
		CalendarID:  calID,
		UserID:      userID,
		Role:        calendar.CalendarMembersRoleViewer,
		MemberColor: "#EA4335",
	})
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
func ensureDefaults(ctx context.Context, q *generated.Queries, cq *calendar.Queries, workspaceID, userID uint32) {
	profile, err := q.FindUserProfileById(ctx, userID)
	if err != nil {
		slog.WarnContext(ctx, "failed to load user profile for auto-create", "err", err)
		return
	}
	if err := EnsurePersonalCalendar(ctx, cq, workspaceID, userID, profile.DisplayName); err != nil {
		slog.WarnContext(ctx, "failed to auto-create personal calendar", "err", err)
	}
	if err := EnsureSystemCalendars(ctx, q, cq, workspaceID, userID); err != nil {
		slog.WarnContext(ctx, "failed to auto-create system calendars", "err", err)
	}
}
