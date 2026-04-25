package itemkit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Flag keys recognized in calendar_events.flags JSON. Unknown keys are
// preserved on write — UI ignores them.
const (
	// FlagNonWorkingDay is set when an event's start_at lands on a day
	// the actor's effective working_days string marks as off, AND the
	// actor's snap_to_working_day mode is "warn".
	FlagNonWorkingDay = "non_working_day"

	// FlagAutoSnapped is set when itemkit has forward-snapped an event
	// from a non-working day to the next working day under snap mode
	// "auto". The neighbouring keys carry the source/target ISO dates.
	FlagAutoSnapped       = "auto_snapped"
	FlagAutoSnappedFrom   = "auto_snapped_from"
	FlagAutoSnappedTo     = "auto_snapped_to"
	FlagAutoSnappedReason = "auto_snapped_reason"
)

// loadEventFlags reads the calendar_events.flags JSON column for the
// given event. Returns an empty map when the column is NULL or empty.
func loadEventFlags(ctx context.Context, tx TX, eventID uint32) (map[string]any, error) {
	var raw sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT flags FROM calendar_events WHERE id = ?`, eventID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("itemkit: load flags: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		// Corrupt JSON should not block a write; log-in-place and treat
		// as empty so updates proceed.
		return map[string]any{}, nil
	}
	return out, nil
}

// writeEventFlags serialises the map and writes it to calendar_events.flags.
// A nil / empty map is stored as SQL NULL so the column tracks "no flags"
// explicitly rather than an empty JSON object.
func writeEventFlags(ctx context.Context, tx TX, eventID uint32, flags map[string]any) error {
	var val sql.NullString
	if len(flags) > 0 {
		b, err := json.Marshal(flags)
		if err != nil {
			return fmt.Errorf("itemkit: marshal flags: %w", err)
		}
		val = sql.NullString{String: string(b), Valid: true}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET flags = ? WHERE id = ?`, val, eventID); err != nil {
		return fmt.Errorf("itemkit: write flags: %w", err)
	}
	return nil
}

// mergeFlags returns a copy of base with the entries of overlay applied
// on top. Overlay values of nil remove the key from the result. Unknown
// keys in base are preserved.
func mergeFlags(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}
