package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func ensurePersonalCalendar(ctx context.Context, db *sql.DB, wsID, userID uint32, name string, logger *slog.Logger) (uint32, error) {
	var calID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendars
		 WHERE workspace_id = ? AND kind = 'personal' AND owner_user_id = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, userID,
	).Scan(&calID)
	if err == nil {
		logger.Info("calendar exists", "calendar_id", calID, "user_id", userID)
		return calID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	pub := types.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, owner_user_id)
		 VALUES (?, ?, 'personal', ?, '#4285F4', ?)`,
		pub, wsID, name, userID,
	)
	if err != nil {
		return 0, err
	}
	newID64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	logger.Info("created personal calendar", "id", newID64, "user_id", userID)
	return uint32(newID64), nil //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureHolidayCalendar(ctx context.Context, db *sql.DB, wsID uint32, name string, logger *slog.Logger) (uint32, error) {
	const slug = "holidays.jp"
	var calID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendars
		 WHERE workspace_id = ? AND kind = 'system' AND system_slug = ? AND enabled = TRUE
		 LIMIT 1`,
		wsID, slug,
	).Scan(&calID)
	if err == nil {
		logger.Info("holiday calendar exists", "calendar_id", calID)
		return calID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	pub := types.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, system_slug)
		 VALUES (?, ?, 'system', ?, '#DC2626', ?)`,
		pub, wsID, name, slug,
	)
	if err != nil {
		return 0, err
	}
	newID64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	logger.Info("created holiday calendar", "id", newID64, "slug", slug)
	return uint32(newID64), nil //#nosec G115 -- LastInsertId for calendars.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureSubscription(ctx context.Context, db *sql.DB, wsID, calID, userID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM calendar_subscriptions WHERE calendar_id = ? AND user_id = ? AND workspace_id = ?",
		calID, userID, wsID,
	).Scan(&existing)
	if err == nil {
		logger.Info("calendar subscription exists", "calendar_id", calID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	pub := types.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO calendar_subscriptions (public_id, calendar_id, user_id, workspace_id, display_color, visible, sort_weight)
		 VALUES (?, ?, ?, ?, '#4285F4', TRUE, 0)`,
		pub, calID, userID, wsID,
	); err != nil {
		return err
	}
	logger.Info("created calendar subscription", "calendar_id", calID, "user_id", userID)
	return nil
}

// ensureCalendarMember grants a user access to a calendar. This is the
// row calendar listing is driven from — a subscription holds display
// preferences and grants nothing, so a seeded calendar without a
// membership row is invisible to the very user it was seeded for.
// Idempotent on (calendar_id, user_id) via the table's unique key.
func ensureCalendarMember(ctx context.Context, db *sql.DB, wsID, calID, userID uint32, role string, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM calendar_members WHERE calendar_id = ? AND user_id = ? AND workspace_id = ?",
		calID, userID, wsID,
	).Scan(&existing)
	if err == nil {
		logger.Info("calendar membership exists", "calendar_id", calID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	pub := types.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, calendar_id, user_id, workspace_id, role, member_color)
		 VALUES (?, ?, ?, ?, ?, '#4285F4')`,
		pub, calID, userID, wsID, role,
	); err != nil {
		return err
	}
	logger.Info("created calendar membership", "calendar_id", calID, "user_id", userID, "role", role)
	return nil
}

// findEventIDByTitle matches seed events by (calendar_id, title) since
// calendar_events has no slug. Seed titles are unique per calendar.
func findEventIDByTitle(ctx context.Context, db *sql.DB, calID uint32, title string) (uint32, error) {
	var id uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_events
		 WHERE calendar_id = ? AND title = ? AND enabled = TRUE
		 ORDER BY id ASC LIMIT 1`,
		calID, title,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// nextNoonUTC returns today at 12:00 UTC if the current time is before
// that, otherwise tomorrow at 12:00 UTC. Keeps the sample event in the
// future for a consistent demo.
func nextNoonUTC() time.Time {
	now := time.Now().UTC()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if !noon.After(now) {
		noon = noon.Add(24 * time.Hour)
	}
	return noon
}

func ensureSampleEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title, location string, logger *slog.Logger) (uint32, error) {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("sample event exists", "id", existing, "title", title)
		return existing, nil
	}
	start := nextNoonUTC()
	end := start.Add(time.Hour)
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindEvent,
		Visibility:      calendar.CalendarEventsVisibilityPublic,
		ShowAs:          calendar.CalendarEventsShowAsBusy,
		Flexibility:     calendar.CalendarEventsFlexibilityFixed,
		Title:           title,
		AllDay:          false,
		StartAt:         sql.NullTime{Time: start, Valid: true},
		EndAt:           sql.NullTime{Time: end, Valid: true},
		Timezone:        "UTC",
		Location:        sql.NullString{String: location, Valid: location != ""},
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created sample event", "id", id, "title", title, "start_at", start.Format(time.RFC3339))
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_events.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureUndatedEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title, memo string, logger *slog.Logger) (uint32, error) {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return 0, err
	} else if existing > 0 {
		logger.Info("undated event exists", "id", existing, "title", title)
		return existing, nil
	}
	// Probe whether calendar_events.start_at is nullable in the currently
	// connected MySQL instance. Older local containers were booted against
	// an earlier schema that declared start_at as NOT NULL, and compose
	// only seeds schema.sql on an empty data volume — so stale DBs survive
	// schema edits silently. Rather than hard-failing the entire seed run,
	// warn the operator and skip this single fixture; a fresh
	// `make db-reset` (or `make db-apply` for self-hosted MySQL) picks up
	// the nullable column and restores the undated-event fixture on the
	// next run.
	var isNullable string
	probeErr := db.QueryRowContext(ctx, `
		SELECT IS_NULLABLE
		FROM information_schema.columns
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = 'calendar_events'
		  AND COLUMN_NAME = 'start_at'
	`).Scan(&isNullable)
	if probeErr != nil {
		return 0, fmt.Errorf("probe calendar_events.start_at nullability: %w", probeErr)
	}
	if isNullable != "YES" {
		logger.Warn(
			"skipping undated event: calendar_events.start_at is NOT NULL in this DB; run `make db-reset` (or `make db-apply` for self-hosted MySQL) to pick up the nullable schema",
			"title", title,
		)
		return 0, nil
	}
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindEvent,
		Visibility:      calendar.CalendarEventsVisibilityPrivate,
		ShowAs:          calendar.CalendarEventsShowAsFree,
		Flexibility:     calendar.CalendarEventsFlexibilityFixed,
		Title:           title,
		AllDay:          false,
		StartAt:         sql.NullTime{},
		EndAt:           sql.NullTime{},
		Timezone:        "UTC",
		Memo:            sql.NullString{String: memo, Valid: memo != ""},
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return 0, err
	}
	logger.Info("created undated event", "id", id, "title", title)
	return uint32(id), nil //#nosec G115 -- LastInsertId for calendar_events.id (BIGINT UNSIGNED), fits uint32 in dev seed
}

func ensureHolidayEvent(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, calID, ownerID uint32, title string, logger *slog.Logger) error {
	if existing, err := findEventIDByTitle(ctx, db, calID, title); err != nil {
		return err
	} else if existing > 0 {
		logger.Info("holiday event exists", "id", existing, "title", title)
		return nil
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	id, err := cq.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:        types.New(),
		WorkspaceID:     wsID,
		CalendarID:      calID,
		Kind:            calendar.CalendarEventsKindBlock,
		Visibility:      calendar.CalendarEventsVisibilityPublic,
		ShowAs:          calendar.CalendarEventsShowAsFree,
		Flexibility:     calendar.CalendarEventsFlexibilityFixed,
		Title:           title,
		AllDay:          true,
		StartAt:         sql.NullTime{Time: start, Valid: true},
		EndAt:           sql.NullTime{Time: end, Valid: true},
		Timezone:        "UTC",
		OwnerUserID:     ownerID,
		CreatedByUserID: ownerID,
	})
	if err != nil {
		return err
	}
	logger.Info("created holiday event", "id", id, "title", title)
	return nil
}

func ensureAttendee(ctx context.Context, db *sql.DB, cq *calendar.Queries, wsID, eventID, userID uint32, logger *slog.Logger) error {
	var existing uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM calendar_event_attendees
		 WHERE event_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`,
		eventID, userID,
	).Scan(&existing)
	if err == nil {
		logger.Info("attendee exists", "event_id", eventID, "user_id", userID)
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := cq.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
		PublicID:    types.New(),
		WorkspaceID: wsID,
		EventID:     sql.NullInt32{Int32: int32(eventID), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic deployments
		UserID:      userID,
		Rsvp:        calendar.CalendarEventAttendeesRsvpPending,
		CanEdit:     false,
	}); err != nil {
		return err
	}
	logger.Info("created attendee", "event_id", eventID, "user_id", userID)
	return nil
}
