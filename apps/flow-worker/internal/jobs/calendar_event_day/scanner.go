// Package calendar_event_day implements the worker job that emits one
// calendar.event_day_arrived signal per (workspace, event, event_day)
// tuple when an event's start_date arrives in the workspace's local
// timezone. The job is hosted by apps/flow-worker as a concrete
// signal materialiser.
//
// The package is split into:
//
//   - scanner.go  — read-side: list workspaces, list the events whose
//     local day arrives in the current tick window per workspace. Uses
//     database/sql directly (not sqlc) because the queries are
//     workflow-specific and adding the worker to sqlc.yaml would expand
//     the codegen surface for one or two queries.
//   - client.go   — write-side: HTTP POST against flow-api /signals with
//     the worker service-token bearer.
//   - job.go      — the Job interface implementation that wires Scanner +
//     SignalsClient and increments the obs counters.
//
// Fire-once semantics: a naive scan of "today's events" re-posts the same
// signal on every tick for the whole local day (~1440 POST/event/day at a
// 60s cadence). Instead the scan fires an event-day only when that local
// day's midnight boundary falls inside the tick window
// `[now - interval - catch-up, now)`, so each (event, local-day) emits at
// most once at day arrival. The day-scoped external_id keeps the emit
// idempotent across replicas and across a catch-up backfill.
//
// Recurring events: a row carrying a recurrence_rule is expanded
// worker-side so every occurrence whose local day arrives in the tick
// window emits its own event_day_arrived (not just the base start day).
// The expander honours interval / count / until / byDay / byMonthDay plus
// the recurrence_end column and recurrence_exceptions exclusions, and
// advances occurrences in the event timezone so DST does not drift a
// recurring meeting. The day-scoped external_id keeps each
// (event, occurrence-day) idempotent across ticks and catch-up. See
// ListEventsForDays and expandOccurrences (recurrence.go) for the
// supported-rule boundary.
//
// All public_id values are surfaced as canonical UUID v7 strings so that
// internal numeric ids never leak past the worker → API boundary
// (CLAUDE.md rule 18).
package calendar_event_day

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// Scanner reads the worker's MySQL view of workspaces and calendar_events.
//
// Scanner does not own the *sql.DB pool — the lifecycle wiring in
// cmd/worker passes a shared pool so connection limits are coordinated
// with future jobs.
type Scanner struct {
	// DB is the MySQL pool used for read queries. Required.
	DB *sql.DB
	// Logger receives per-row scan failures. Optional for tests.
	Logger *slog.Logger
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

// candidateRow is the raw read shape backing one scanned calendar_events
// row before it is expanded into (event, occurrence-day) tuples. It carries
// the recurrence columns so a recurring row's occurrences can be projected
// Go-side without a second query.
type candidateRow struct {
	event                Event
	timezone             string
	recurrenceRule       []byte
	recurrenceEnd        sql.NullTime
	recurrenceExceptions []byte
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

// EventOnDay pairs a scanned event with the workspace-local day whose
// arrival triggered it. day is the YYYY-MM-DD string that flows into the
// dedupe key and the signal's expiresAt; carrying it alongside the row
// keeps a catch-up tick (which materialises more than one local day in a
// single pass) from mislabelling backfilled rows with "today".
type EventOnDay struct {
	// Event is the calendar_events row whose local day just arrived.
	Event Event
	// Day is the workspace-local YYYY-MM-DD the row belongs to.
	Day string
	// ExpiresAtUnix is the unix-seconds instant the row's workspace-local
	// day ends (= next local midnight, projected to UTC). It feeds the
	// signal's expiresAt so a matured event-day stops being a retention
	// candidate once the day it describes rolls over. Computed per-row so
	// a catch-up backfill expires each day correctly, not relative to the
	// tick's "today".
	ExpiresAtUnix int64
}

// ListEventsForDays returns the enabled calendar_events rows whose local
// day arrives within the current tick window, scoped to one workspace.
//
// Fire-once: only the local day(s) whose midnight boundary falls inside
// `[now - window, now)` are materialised, so a steady 60s cadence emits a
// given (event, day) exactly once at day arrival rather than ~1440 times
// across the day. `window` is the tick interval widened by the catch-up
// lookback; see localDaysArriving.
//
// Catch-up: a wider window (set from Job.CatchUpWindow) re-materialises
// the local days a worker outage skipped. The day-scoped external_id
// makes the backfill idempotent — flow-api's INSERT IGNORE collapses any
// day already emitted before the outage.
//
// Parent soft-delete: the INNER JOIN on calendars enforces
// `c.enabled = TRUE`, so events whose parent calendar was soft-deleted
// are never scanned (the judge must not run on a deleted calendar's
// events). Workspace scoping is asserted on both ce and c.
//
// Recurring events: a row carrying a recurrence_rule is expanded Go-side.
// For each arriving day the scan walks the rule's occurrences (honouring
// interval / count / until / byDay / byMonthDay, the recurrence_end
// column, and the recurrence_exceptions exclusions) and emits one
// (event, occurrence-day) tuple per occurrence whose UTC start falls in
// that day's range. Occurrences advance in the event timezone so a daily
// or weekly meeting keeps its wall-clock time across a DST transition.
// The day-scoped external_id keeps each occurrence-day idempotent across
// ticks and catch-up. See expandOccurrences for the supported-rule
// boundary (BYSETPOS is not applied, mirroring the client expander).
//
// The local-day boundary is converted to its UTC range Go-side and the
// query uses a plain half-open interval `[utcStart, utcEnd)` per day.
// This avoids depending on the MySQL timezone tables (CONVERT_TZ), which
// are not loaded by default in the mysql:9.6 compose image; the workspace
// timezone is the single source of truth for the local date. A recurring
// row is selected as a candidate whenever its base start_at is before the
// day's upper bound (occurrences only move forward), then filtered down to
// the day's range by the expansion.
func (s *Scanner) ListEventsForDays(ctx context.Context, workspaceID uint32, tz string, now time.Time, window time.Duration) ([]EventOnDay, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("load workspace timezone %q: %w", tz, err)
	}

	days := localDaysArriving(now, window, loc)
	if len(days) == 0 {
		return nil, nil
	}

	// A non-recurring row qualifies only when its base start_at lands in
	// the day range. A recurring row qualifies whenever its base start_at
	// precedes the day's upper bound — a later occurrence may still land in
	// the day even though the base is earlier; the Go-side expansion makes
	// the final per-day decision.
	const q = `
		SELECT ce.id, ce.public_id, ce.start_at, ce.all_day,
		       ce.timezone, ce.recurrence_rule, ce.recurrence_end, ce.recurrence_exceptions
		FROM calendar_events ce
		INNER JOIN calendars c
		        ON c.id = ce.calendar_id
		       AND c.enabled = TRUE
		       AND c.workspace_id = ce.workspace_id
		WHERE ce.workspace_id = ?
		  AND ce.enabled = TRUE
		  AND ce.start_at IS NOT NULL
		  AND ce.start_at < ?
		  AND (
		        ce.recurrence_rule IS NOT NULL
		     OR ce.start_at >= ?
		  )
		ORDER BY ce.start_at ASC
	`

	var out []EventOnDay
	for _, d := range days {
		rows, err := s.DB.QueryContext(ctx, q, workspaceID, d.utcEnd, d.utcStart)
		if err != nil {
			return nil, fmt.Errorf("query events arriving on %s: %w", d.day, err)
		}
		for rows.Next() {
			var c candidateRow
			if scanErr := rows.Scan(
				&c.event.ID, &c.event.PublicID, &c.event.StartAt, &c.event.AllDay,
				&c.timezone, &c.recurrenceRule, &c.recurrenceEnd, &c.recurrenceExceptions,
			); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan event row: %w", scanErr)
			}
			tuples, expandErr := s.expandCandidate(c, d)
			if expandErr != nil {
				// A single malformed rule must not abort the workspace scan.
				// Log the bad row and keep scanning later events in the same
				// workspace so one typo cannot permanently block the day.
				if s.Logger != nil {
					s.Logger.WarnContext(ctx, "calendar_event_day: expand event failed",
						slog.Any("err", expandErr),
						slog.Uint64("workspace_internal_id", uint64(workspaceID)),
						slog.String("event_public_id", c.event.PublicID.String()),
						slog.String("event_day", d.day),
					)
				}
				continue
			}
			out = append(out, tuples...)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate event rows: %w", rowsErr)
		}
		_ = rows.Close()
	}
	return out, nil
}

// expandCandidate turns one scanned candidateRow into the (event, day)
// tuples it contributes for a single arriving day. A non-recurring row
// contributes exactly one tuple (its base start already passed the day
// range filter in SQL). A recurring row contributes one tuple per
// occurrence whose UTC start lands in the day's range; an occurrence
// excluded by recurrence_exceptions or falling past recurrence_end / until
// contributes none.
func (s *Scanner) expandCandidate(c candidateRow, d arrivingDay) ([]EventOnDay, error) {
	rule, err := parseRecurrenceRule(c.recurrenceRule)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return []EventOnDay{{
			Event:         c.event,
			Day:           d.day,
			ExpiresAtUnix: d.utcEnd.Unix(),
		}}, nil
	}

	// Anchor occurrence arithmetic to the event timezone so wall-clock
	// time-of-day is preserved across DST; fall back to UTC when the
	// stored zone is unknown rather than dropping the event.
	eventLoc, err := time.LoadLocation(c.timezone)
	if err != nil {
		eventLoc = time.UTC
	}

	exceptions, err := parseRecurrenceExceptions(c.recurrenceExceptions, eventLoc)
	if err != nil {
		return nil, err
	}

	var until time.Time
	if rule.Until != nil && *rule.Until != "" {
		until = parseRuleUntil(*rule.Until, eventLoc)
	}
	var recurrenceEnd time.Time
	if c.recurrenceEnd.Valid {
		recurrenceEnd = c.recurrenceEnd.Time.UTC()
	}

	occurrences := expandOccurrences(
		rule, c.event.StartAt.UTC(), eventLoc,
		recurrenceEnd, until, exceptions,
		d.utcStart, d.utcEnd,
	)
	if len(occurrences) == 0 {
		return nil, nil
	}

	out := make([]EventOnDay, 0, len(occurrences))
	for _, occ := range occurrences {
		ev := c.event
		ev.StartAt = occ
		out = append(out, EventOnDay{
			Event:         ev,
			Day:           d.day,
			ExpiresAtUnix: d.utcEnd.Unix(),
		})
	}
	return out, nil
}

// parseRuleUntil parses the recurrence rule's `until` field, accepting an
// RFC 3339 timestamp or a bare YYYY-MM-DD date (interpreted in loc, the
// event timezone). A bare date names the whole local day as the inclusive
// upper bound, so it resolves to the final instant of that local day
// (23:59:59.999999999 local) rather than its midnight: because
// expandOccurrences treats until as an inclusive upper bound on the
// candidate instant, an occurrence at any wall-clock time on the until day
// still qualifies. Resolving to midnight would drop occurrences whose
// time-of-day is past 00:00. Returns the zero time when neither form
// parses, so an unparseable until simply leaves the sequence unbounded by
// until.
func parseRuleUntil(value string, loc *time.Location) time.Time {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC()
	}
	if t, err := time.ParseInLocation("2006-01-02", value, loc); err == nil {
		// Extend the bare date to the end of that local day so the until
		// day is inclusive across every wall-clock time-of-day; stepping to
		// the next local midnight and backing off a nanosecond keeps the
		// boundary correct across DST transitions.
		return t.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC()
	}
	return time.Time{}
}

// arrivingDay describes a single workspace-local day whose midnight
// boundary falls inside the current tick window. day is the YYYY-MM-DD
// label; [utcStart, utcEnd) is the UTC range that day projects to.
type arrivingDay struct {
	day      string
	utcStart time.Time
	utcEnd   time.Time
}

// localDaysArriving returns the workspace-local days whose start-of-day
// (local midnight) falls inside the half-open tick window
// `[now - window, now)`, ordered oldest-first.
//
// In steady state (window = tick interval, e.g. 60s) at most one day
// qualifies: the day whose midnight the tick just crossed. Mid-day ticks
// return nothing, which is what makes the emit fire-once-per-day instead
// of every tick. When window is widened by the catch-up lookback the
// function also yields the local days a recent outage skipped, so the
// caller can backfill them; the day-scoped dedupe key keeps that
// idempotent.
//
// The walk steps backwards a bounded number of local days from `now`
// (capped so a pathologically large window cannot fan out without limit)
// and keeps each day whose local midnight lands in the window.
func localDaysArriving(now time.Time, window time.Duration, loc *time.Location) []arrivingDay {
	if window <= 0 {
		return nil
	}
	windowStart := now.Add(-window)

	// Cap the backward walk so a misconfigured window can never scan an
	// unbounded number of days. 400 covers a year of catch-up, far beyond
	// any sane outage.
	const maxDays = 400

	var out []arrivingDay
	local := now.In(loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < maxDays; i++ {
		midnightUTC := dayStart.UTC()
		if midnightUTC.Before(windowStart) {
			// This day's midnight predates the window; every earlier day
			// does too, so stop walking.
			break
		}
		// Keep the day when its local midnight is in [windowStart, now).
		if midnightUTC.Before(now) {
			start, end := localDayUTCRange(dayStart, loc)
			out = append(out, arrivingDay{
				day:      dayStart.Format("2006-01-02"),
				utcStart: start,
				utcEnd:   end,
			})
		}
		// Step to the previous local midnight via time.Date so DST
		// transitions are normalised by the tz database.
		dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day()-1, 0, 0, 0, 0, loc)
	}

	// Reverse to oldest-first so a catch-up pass materialises days in
	// chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
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
