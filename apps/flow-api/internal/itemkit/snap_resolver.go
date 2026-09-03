package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/holidays"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// ResolveSnapConfig reads the user and workspace rows to assemble a
// SnapConfig suitable for passing into ScheduleTask / RescheduleEvent /
// RescheduleTask, including the holiday set implied by the actor's
// effective country when users.treat_holidays_as_non_working is on.
//
// When the actor has no row (system actions, bootstrap), the returned
// config defaults to SnapOff so no badges are applied.
func ResolveSnapConfig(ctx context.Context, tx TX, workspaceID, userID uint32) (SnapConfig, error) {
	cfg := SnapConfig{Mode: region.SnapOff}
	if userID == 0 || workspaceID == 0 {
		return cfg, nil
	}

	var wsDays string
	var wsTZ, wsCountry sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT working_days, timezone, country FROM workspaces WHERE id = ? AND enabled = TRUE`,
		workspaceID).Scan(&wsDays, &wsTZ, &wsCountry)
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
		userCountry   sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT working_days, snap_to_working_day, treat_holidays_as_non_working, timezone, country
		 FROM users WHERE id = ? AND enabled = TRUE`,
		userID).Scan(&userDays, &snapMode, &treatHolidays, &userTZ, &userCountry)
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
	// The holiday set is loaded only when the actor asked for it. Country
	// falls back to the workspace's the same way timezone does, so a user
	// who never set a personal country still gets their organisation's
	// calendar rather than none at all.
	if cfg.TreatHolidays {
		cfg.Holidays = holidays.Set(effectiveCountry(userCountry, wsCountry))
	}

	// The same chain the calendar handlers and the MCP tools apply, run
	// through the one resolver rather than restated here. Restated, it
	// drifted: this copy fell through to the next candidate on an
	// unresolvable name while the others errored, so a user whose stored
	// zone had been retired got their workspace's working week while
	// every other surface refused the row.
	z, zerr := region.Resolve(valueOrEmpty(userTZ), valueOrEmpty(wsTZ))
	if zerr != nil {
		return cfg, fmt.Errorf("itemkit: resolve timezone for user %d: %w", userID, zerr)
	}
	cfg.Zone = z
	return cfg, nil
}

// effectiveCountry picks the ISO 3166-1 alpha-2 code whose holidays
// apply to the actor: their own if set, otherwise the workspace's.
func effectiveCountry(userCountry, wsCountry sql.NullString) string {
	if userCountry.Valid && userCountry.String != "" {
		return userCountry.String
	}
	if wsCountry.Valid && wsCountry.String != "" {
		return wsCountry.String
	}
	return ""
}

func valueOrEmpty(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
