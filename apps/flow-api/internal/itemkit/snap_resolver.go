package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// ResolveSnapConfig reads the user and workspace rows to assemble a
// SnapConfig suitable for passing into ScheduleTask / RescheduleEvent /
// RescheduleTask. It does NOT populate Holidays — callers that want
// holiday-aware snap supply the set separately (see LoadHolidayDates).
//
// When the actor has no row (system actions, bootstrap), the returned
// config defaults to SnapOff so no badges are applied.
func ResolveSnapConfig(ctx context.Context, tx TX, workspaceID, userID uint32) (SnapConfig, error) {
	cfg := SnapConfig{Mode: region.SnapOff}
	if userID == 0 || workspaceID == 0 {
		return cfg, nil
	}

	var wsDays string
	var wsTZ sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT working_days, timezone FROM workspaces WHERE id = ? AND enabled = TRUE`,
		workspaceID).Scan(&wsDays, &wsTZ)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("itemkit: load workspace %d for snap: %w", workspaceID, err)
	}

	var (
		userDays      sql.NullString
		snapMode      sql.NullString
		treatHolidays sql.NullBool
		userTZ        sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT working_days, snap_to_working_day, treat_holidays_as_non_working, timezone
		 FROM users WHERE id = ? AND enabled = TRUE`,
		userID).Scan(&userDays, &snapMode, &treatHolidays, &userTZ)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("itemkit: load user %d for snap: %w", userID, err)
	}

	cfg.Mode = region.SnapWarn
	if snapMode.Valid {
		cfg.Mode = region.ParseSnapMode(snapMode.String)
	}
	cfg.WorkingDays = region.EffectiveWorkingDays(
		valueOrEmpty(userDays), wsDays,
	)
	cfg.TreatHolidays = treatHolidays.Valid && treatHolidays.Bool

	loc := time.UTC
	if userTZ.Valid && userTZ.String != "" {
		if parsed, err := time.LoadLocation(userTZ.String); err == nil {
			loc = parsed
		}
	} else if wsTZ.Valid && wsTZ.String != "" {
		if parsed, err := time.LoadLocation(wsTZ.String); err == nil {
			loc = parsed
		}
	}
	cfg.Location = loc
	return cfg, nil
}

// LoadHolidayDates returns the set of ISO-date strings (YYYY-MM-DD)
// covered by events on system (holiday) calendars the user is
// subscribed to, within the inclusive [from, to] range. Empty when the
// user has no holiday subscription. Used by handlers that want snap to
// treat subscribed holidays as non-working days.
func LoadHolidayDates(ctx context.Context, tx TX, userID, workspaceID uint32, from, to time.Time) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if userID == 0 || workspaceID == 0 {
		return out, nil
	}
	const q = `
		SELECT DATE(ce.start_at) AS d
		FROM calendar_events ce
		JOIN calendars c ON c.id = ce.calendar_id AND c.enabled = TRUE AND c.kind = 'system'
		JOIN calendar_subscriptions cs
		  ON cs.calendar_id = c.id AND cs.user_id = ? AND cs.enabled = TRUE
		WHERE ce.enabled = TRUE
		  AND ce.start_at IS NOT NULL
		  AND ce.start_at >= ? AND ce.start_at < ?`
	rows, err := tx.QueryContext(ctx, q, userID, from, to.Add(24*time.Hour))
	if err != nil {
		return out, fmt.Errorf("itemkit: load holiday dates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return out, fmt.Errorf("itemkit: scan holiday date: %w", err)
		}
		out[d.Format("2006-01-02")] = struct{}{}
	}
	return out, rows.Err()
}

func valueOrEmpty(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
