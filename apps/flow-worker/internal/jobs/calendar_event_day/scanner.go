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
// The expansion is packages/go-shared/recurrence, the same code the agent
// surface and the notification scheduler expand with, so the days this
// job fires on and the days those two report are one answer rather than
// three that happen to agree. The day-scoped external_id keeps each
// (event, occurrence-day) idempotent across ticks and catch-up.
//
// All public_id values are surfaced as canonical UUID v7 strings so that
// internal numeric ids never leak past the worker → API boundary
// (CLAUDE.md rule 18).
package calendar_event_day

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
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
	event Event
	// endAt is the event's UTC end. The expander needs the duration to
	// decide which occurrences meet a window, so the scan reads it even
	// though the emitted tuple carries only the start.
	endAt                sql.NullTime
	timezone             string
	recurrenceRule       []byte
	recurrenceEnd        sql.NullTime
	recurrenceExceptions []byte
	// overriddenStarts are the occurrence starts an override row already
	// stands in for, in the spelling the expander parses. Filled from a
	// single batch read over the whole scan, so it is empty on a row with
	// no rule and on a series nobody has edited an occurrence of.
	overriddenStarts []string
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
// Recurring events: a row carrying a recurrence_rule is expanded Go-side
// through packages/go-shared/recurrence, and the scan emits one
// (event, occurrence-day) tuple per occurrence whose UTC start falls in
// that day's range. The supported-rule boundary and the arithmetic are
// that package's, so the days this job fires on are the days the agent
// surface and the calendar report for the same series. The day-scoped
// external_id keeps each occurrence-day idempotent across ticks and
// catch-up.
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
	z, err := region.Resolve(tz)
	if err != nil {
		return nil, fmt.Errorf("load workspace timezone %q: %w", tz, err)
	}

	days := localDaysArriving(now, window, z)
	if len(days) == 0 {
		return nil, nil
	}

	// A non-recurring row qualifies only when its base start_at lands in
	// the day range. A recurring row qualifies whenever its base start_at
	// precedes the day's upper bound — a later occurrence may still land in
	// the day even though the base is earlier; the Go-side expansion makes
	// the final per-day decision.
	const q = `
		SELECT ce.id, ce.public_id, ce.start_at, ce.end_at, ce.all_day,
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

	// Candidates for every arriving day are read first so the override
	// lookup below is a single query for the whole scan rather than one per
	// series.
	type dayCandidate struct {
		row candidateRow
		day arrivingDay
	}
	var candidates []dayCandidate
	for _, d := range days {
		rows, err := s.DB.QueryContext(ctx, q, workspaceID, d.utcEnd, d.utcStart)
		if err != nil {
			return nil, fmt.Errorf("query events arriving on %s: %w", d.day, err)
		}
		for rows.Next() {
			var c candidateRow
			if scanErr := rows.Scan(
				&c.event.ID, &c.event.PublicID, &c.event.StartAt, &c.endAt, &c.event.AllDay,
				&c.timezone, &c.recurrenceRule, &c.recurrenceEnd, &c.recurrenceExceptions,
			); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan event row: %w", scanErr)
			}
			candidates = append(candidates, dayCandidate{row: c, day: d})
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate event rows: %w", rowsErr)
		}
		_ = rows.Close()
	}

	parentIDs := make([]uint32, 0, len(candidates))
	seen := make(map[uint32]bool, len(candidates))
	for _, c := range candidates {
		// Only a row carrying a rule can be overridden; an override names
		// its master, never another override.
		if len(c.row.recurrenceRule) == 0 || seen[c.row.event.ID] {
			continue
		}
		seen[c.row.event.ID] = true
		parentIDs = append(parentIDs, c.row.event.ID)
	}
	overridden, err := s.listOverriddenStarts(ctx, workspaceID, parentIDs)
	if err != nil {
		return nil, err
	}

	var out []EventOnDay
	for _, c := range candidates {
		c.row.overriddenStarts = overridden[c.row.event.ID]
		tuples, expandErr := s.expandCandidate(c.row, c.day)
		if expandErr != nil {
			// A single malformed rule must not abort the workspace scan.
			// Log the bad row and keep scanning later events in the same
			// workspace so one typo cannot permanently block the day.
			if s.Logger != nil {
				s.Logger.WarnContext(ctx, "calendar_event_day: expand event failed",
					slog.Any("err", expandErr),
					logutil.LogNumber("workspace_internal_id", workspaceID),
					slog.String("event_public_id", c.row.event.PublicID.String()),
					slog.String("event_day", c.day.day),
				)
			}
			continue
		}
		out = append(out, tuples...)
	}
	return out, nil
}

// listOverriddenStarts returns, per master id, the occurrence starts a
// live override row already stands in for, spelled as RFC 3339 UTC so the
// expander's exception parser resolves them to the same instants it
// generates.
//
// The read is deliberately unfiltered by date: an override may be moved
// anywhere, including outside the day being scanned, and it still replaces
// the occurrence it names. Filtering by the override's own start would let
// the master announce a day for an occurrence that moved away.
//
// It is unfiltered by visibility, and must be. This scan decides whether a
// meeting happens on a day, not what to show a person: a signal is not
// addressed to anybody, so there is no viewer whose confidential rows to
// withhold. Scoping the subtraction by visibility would leave the master
// announcing a day for an occurrence that is not there, and would do it
// exactly when the replacement is confidential — so it must read every
// live override of these masters, whoever owns them. The viewer-scoped
// query on the calendar read paths answers a different question and does
// not belong here.
func (s *Scanner) listOverriddenStarts(
	ctx context.Context,
	workspaceID uint32,
	parentIDs []uint32,
) (map[uint32][]string, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}

	//#nosec G202 -- the interpolated text is a generated ?-placeholder list sized from len(parentIDs); every id travels as a bound argument
	q := `
		SELECT ov.recurrence_parent_id, ov.recurrence_original_start
		FROM calendar_events ov
		WHERE ov.workspace_id = ?
		  AND ov.enabled = TRUE
		  AND ov.recurrence_original_start IS NOT NULL
		  AND ov.recurrence_parent_id IN (?` + strings.Repeat(",?", len(parentIDs)-1) + `)`

	args := make([]any, 0, len(parentIDs)+1)
	args = append(args, workspaceID)
	for _, id := range parentIDs {
		args = append(args, id)
	}

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query overridden occurrence starts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[uint32][]string{}
	for rows.Next() {
		var (
			parentID uint32
			start    time.Time
		)
		if scanErr := rows.Scan(&parentID, &start); scanErr != nil {
			return nil, fmt.Errorf("scan overridden occurrence start: %w", scanErr)
		}
		out[parentID] = append(out[parentID], start.UTC().Format(time.RFC3339))
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate overridden occurrence starts: %w", rowsErr)
	}
	return out, nil
}

// expandCandidate turns one scanned candidateRow into the (event, day)
// tuples it contributes for a single arriving day. A non-recurring row
// contributes exactly one tuple (its base start already passed the day
// range filter in SQL). A recurring row contributes one tuple per
// occurrence whose UTC start lands in the day's range; an occurrence
// excluded by recurrence_exceptions, replaced by an override row, or
// falling past recurrence_end / until contributes none.
func (s *Scanner) expandCandidate(c candidateRow, d arrivingDay) ([]EventOnDay, error) {
	rule, err := recurrence.ParseRule(c.recurrenceRule)
	if err != nil {
		return nil, fmt.Errorf("decode recurrence_rule: %w", err)
	}
	if rule == nil {
		return []EventOnDay{{
			Event:         c.event,
			Day:           d.day,
			ExpiresAtUnix: d.utcEnd.Unix(),
		}}, nil
	}
	// A freq outside the grammar cannot be stepped, and expanding it
	// yields nothing — which is indistinguishable from "this series has no
	// occurrence today". Reporting it puts the row in the log instead.
	if !rule.Freq.Valid() {
		return nil, fmt.Errorf("unsupported recurrence freq %q", rule.Freq)
	}

	exceptions, err := decodeRecurrenceExceptions(c.recurrenceExceptions)
	if err != nil {
		return nil, err
	}

	// The expander decides which occurrences meet the window from the
	// occurrence's whole span, so it needs the event's end. start_at and
	// end_at are set or NULL together (chk_calendar_events_start_end_pair)
	// and the query already excludes NULL starts; the fallback keeps a row
	// that somehow lacks an end from expanding with a negative duration.
	endAt := c.event.StartAt
	if c.endAt.Valid {
		endAt = c.endAt.Time
	}

	var seriesEnd *time.Time
	if c.recurrenceEnd.Valid {
		end := c.recurrenceEnd.Time.UTC()
		seriesEnd = &end
	}

	var out []EventOnDay
	for _, occ := range recurrence.Expand(recurrence.Event{
		StartAt:          c.event.StartAt.UTC(),
		EndAt:            endAt.UTC(),
		Timezone:         c.timezone,
		Rule:             rule,
		Exceptions:       exceptions,
		OverriddenStarts: c.overriddenStarts,
		RecurrenceEnd:    seriesEnd,
	}, d.utcStart, d.utcEnd) {
		// The expander answers "which occurrences meet this window"; a day
		// arrives for an occurrence that *starts* in it. A meeting that
		// began the previous day and is still running already had its day,
		// and emitting it again would announce the same series twice.
		if occ.StartAt.Before(d.utcStart) {
			continue
		}
		ev := c.event
		ev.StartAt = occ.StartAt.UTC()
		out = append(out, EventOnDay{
			Event:         ev,
			Day:           d.day,
			ExpiresAtUnix: d.utcEnd.Unix(),
		})
	}
	return out, nil
}

// decodeRecurrenceExceptions reads the recurrence_exceptions column into
// the string list the expander resolves.
//
// A malformed value is reported rather than ignored. The scan would
// otherwise expand the series with no exclusions at all and announce a
// meeting somebody cancelled; skipping the row and saying so in the log
// is the failure the operator can act on.
func decodeRecurrenceExceptions(raw []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return nil, fmt.Errorf("decode recurrence_exceptions: %w", err)
	}
	return values, nil
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
func localDaysArriving(now time.Time, window time.Duration, z region.Zone) []arrivingDay {
	if window <= 0 {
		return nil
	}
	windowStart := now.Add(-window)

	// Cap the backward walk so a misconfigured window can never scan an
	// unbounded number of days. 400 covers a year of catch-up, far beyond
	// any sane outage.
	const maxDays = 400

	var out []arrivingDay
	day := region.DayOf(now, z)
	for i := 0; i < maxDays; i++ {
		midnightUTC := day.Start(z).UTC()
		if midnightUTC.Before(windowStart) {
			// This day's midnight predates the window; every earlier day
			// does too, so stop walking.
			break
		}
		// Keep the day when its local midnight is in [windowStart, now).
		if midnightUTC.Before(now) {
			out = append(out, arrivingDay{
				day:      day.String(),
				utcStart: midnightUTC,
				utcEnd:   day.EndExclusive(z).UTC(),
			})
		}
		day = day.AddDays(-1)
	}

	// Reverse to oldest-first so a catch-up pass materialises days in
	// chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// localDayUTCRange returns the half-open UTC range `[start, end)` that
// covers exactly one calendar day in z, where the day is the one `now`
// falls on when read in z.
//
// Example: now = 2026-05-17T15:30:00Z, z = Asia/Tokyo (+09:00) → the
// local day is 2026-05-18, so the function returns
// `[2026-05-17T15:00:00Z, 2026-05-18T15:00:00Z)`.
//
// On a DST transition day the range is 23h (spring forward) or 25h
// (fall back) wide in UTC, matching the local-day length: region.Day
// constructs the upper bound as the *next* local midnight rather than
// adding 24h to the lower one, so the jump is honoured.
func localDayUTCRange(now time.Time, z region.Zone) (time.Time, time.Time) {
	day := region.DayOf(now, z)
	return day.Start(z).UTC(), day.EndExclusive(z).UTC()
}

// eventDayString formats the workspace-local day for the dedupe key as
// YYYY-MM-DD. The value is the calendar date `now` falls on in z, so
// every event emitted during the same local day collapses into the same
// external_id even when the worker restarts mid-day.
func eventDayString(now time.Time, z region.Zone) string {
	return region.DayOf(now, z).String()
}

// endOfDayUnixSeconds returns the unix-seconds timestamp of the instant
// the workspace-local day ends. Used as the signal's `expiresAt` so a
// matured calendar.event_day_arrived stops being a candidate for the
// retention sweep once the day rolls.
func endOfDayUnixSeconds(now time.Time, z region.Zone) int64 {
	_, end := localDayUTCRange(now, z)
	return end.Unix()
}
