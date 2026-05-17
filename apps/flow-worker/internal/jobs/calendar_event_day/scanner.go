// Package calendar_event_day implements the worker job that emits one
// calendar.event_day_arrived signal per (workspace, event, event_day)
// tuple when an event's start_date arrives in the workspace's local
// timezone. The job is the first concrete materialiser hosted by
// apps/flow-worker (Phase 5 / W2 of release-8-signals-and-judge-loop).
//
// The package is split into:
//
//   - scanner.go  — read-side: list workspaces, list today's events per
//     workspace. Uses database/sql directly (not sqlc) because the
//     queries are workflow-specific and adding the worker to sqlc.yaml
//     would expand the codegen surface for one or two queries.
//   - client.go   — write-side: HTTP POST against flow-api /signals with
//     the worker service-token bearer.
//   - job.go      — the Job interface implementation that wires Scanner +
//     SignalsClient and increments the obs counters.
//
// All public_id values are surfaced as canonical UUID v7 strings so that
// internal numeric ids never leak past the worker → API boundary
// (CLAUDE.md rule 18).
package calendar_event_day

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// Scanner reads the worker's MySQL view of workspaces and calendar_events.
//
// Scanner does not own the *sql.DB pool — the lifecycle wiring in
// cmd/worker passes a shared pool so connection limits are coordinated
// with future jobs.
type Scanner struct {
	// DB is the MySQL pool used for read queries. Required.
	DB *sql.DB
}

// Workspace is the subset of workspaces columns the calendar event-day
// scan needs: the internal id (for FK joins against calendar_events),
// the public id (for the signal body workspaceId field), and the IANA
// timezone string (for the per-workspace day boundary computation).
type Workspace struct {
	// ID is workspaces.id (internal, never leaves the worker).
	ID uint32
	// PublicID is the workspace UUID v7 used in API request bodies.
	PublicID dbtype.PublicID
	// Timezone is the IANA timezone identifier (e.g. "Asia/Tokyo").
	Timezone string
}

// Event is the subset of calendar_events columns the scan emits per row.
// PublicID is exposed for the signal body's subjectId field; the internal
// id stays inside the worker scope.
type Event struct {
	// ID is calendar_events.id (internal, never leaves the worker).
	ID uint32
	// PublicID is the calendar event UUID v7 used in dedupe keys + the
	// signal body subjectId.
	PublicID dbtype.PublicID
	// StartAt is the event's UTC start time. The scanner guarantees this
	// is non-zero (rows with NULL start_at are excluded by the query).
	StartAt time.Time
	// AllDay flags an all-day event so the payload can hand the
	// distinction to downstream judges.
	AllDay bool
}

// ListWorkspaces returns every enabled workspace with the data needed to
// compute its local-day boundaries. The query is a constant string and
// runs once per tick (60s by default), so the cost is bounded by the
// total workspace count — fine for the v1 SLO.
func (s *Scanner) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	const q = `SELECT id, public_id, timezone FROM workspaces WHERE enabled = TRUE`

	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Workspace
	for rows.Next() {
		var w Workspace
		if scanErr := rows.Scan(&w.ID, &w.PublicID, &w.Timezone); scanErr != nil {
			return nil, fmt.Errorf("scan workspace row: %w", scanErr)
		}
		out = append(out, w)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate workspace rows: %w", rowsErr)
	}
	return out, nil
}

// ListTodayEvents returns every enabled calendar_events row in the
// workspace whose start_at falls on the local "today" in the workspace
// timezone.
//
// The implementation converts the local-day boundary to its UTC range
// Go-side and queries against calendar_events.start_at with a plain
// half-open interval `[utcStart, utcEnd)`. This avoids depending on the
// MySQL timezone tables (CONVERT_TZ), which are not loaded by default in
// the mysql:9.6 compose image. Correctness is preserved because the
// workspace timezone is the single source of truth for "today" and any
// UTC instant in `[utcStart, utcEnd)` lands on the same local date.
//
// Rows with NULL start_at (planning-stage placeholders) are excluded by
// the WHERE clause; the worker has nothing to emit for them.
//
// `now` is taken as the wall-clock instant the tick observes. Tests
// inject a fixed Now via Job.Now so the day-boundary behaviour can be
// verified without faking the system clock.
func (s *Scanner) ListTodayEvents(ctx context.Context, workspaceID uint32, tz string, now time.Time) ([]Event, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("load workspace timezone %q: %w", tz, err)
	}

	utcStart, utcEnd := localDayUTCRange(now, loc)

	const q = `
		SELECT id, public_id, start_at, all_day
		FROM calendar_events
		WHERE workspace_id = ?
		  AND enabled = TRUE
		  AND start_at IS NOT NULL
		  AND start_at >= ?
		  AND start_at < ?
		ORDER BY start_at ASC
	`

	rows, err := s.DB.QueryContext(ctx, q, workspaceID, utcStart, utcEnd)
	if err != nil {
		return nil, fmt.Errorf("query today events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Event
	for rows.Next() {
		var e Event
		if scanErr := rows.Scan(&e.ID, &e.PublicID, &e.StartAt, &e.AllDay); scanErr != nil {
			return nil, fmt.Errorf("scan event row: %w", scanErr)
		}
		out = append(out, e)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate event rows: %w", rowsErr)
	}
	return out, nil
}

// localDayUTCRange returns the half-open UTC range `[start, end)` that
// covers exactly one calendar day in the supplied location, where the
// day is the one `now` falls on when projected into `loc`.
//
// Example: now = 2026-05-17T15:30:00Z, loc = Asia/Tokyo (+09:00) → the
// local day is 2026-05-18, so the function returns
// `[2026-05-17T15:00:00Z, 2026-05-18T15:00:00Z)`.
//
// On a DST transition day the range is 23h (spring forward) or 25h
// (fall back) wide in UTC, matching the local-day length. The function
// computes the upper bound by constructing the *next* local midnight
// directly rather than adding 24h to the lower bound, so the DST jump
// is honoured.
func localDayUTCRange(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Use day+1 in time.Date so the next midnight is resolved by the
	// tz normaliser; this is what produces the correct 23h / 25h UTC
	// span on DST transition days. Naïvely adding 24h would always
	// yield a 24h UTC interval and miss the spring-forward / fall-back
	// hour.
	dayEnd := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	return dayStart.UTC(), dayEnd.UTC()
}

// eventDayString formats the workspace-local day for the dedupe key as
// YYYY-MM-DD. The value is computed from `now.In(loc)` so every event
// emitted during the same local day collapses into the same external_id
// even when the worker restarts mid-day.
func eventDayString(now time.Time, loc *time.Location) string {
	return now.In(loc).Format("2006-01-02")
}

// endOfDayUnixSeconds returns the unix-seconds timestamp of the instant
// the workspace-local day ends. Used as the signal's `expiresAt` so a
// matured calendar.event_day_arrived stops being a candidate for the
// retention sweep once the day rolls.
func endOfDayUnixSeconds(now time.Time, loc *time.Location) int64 {
	_, end := localDayUTCRange(now, loc)
	return end.Unix()
}
