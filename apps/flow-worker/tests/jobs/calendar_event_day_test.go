package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/jobs/calendar_event_day"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/obs"
)

// silentLogger returns a slog.Logger that drops every record. The
// integration tests assert on database rows and Prometheus counters, not
// on the slog stream — log volume during testcontainer boot would
// otherwise drown the actual failure message in unrelated info logs.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mustLoadLocation loads an IANA timezone or skips the test with a
// tzdata installation hint. Every modern Go toolchain ships an embedded
// fallback (time/tzdata), but a deliberately-stripped builder image or
// an Alpine container without tzdata installed will still fail to
// resolve "Asia/Tokyo". Skipping is friendlier than a confusing nil deref
// inside the job's own time.LoadLocation call.
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata missing zone %q (install tzdata or run with time/tzdata import): %v", name, err)
	}
	return loc
}

// setWorkspaceTimezone is the single SQL escape hatch the suite uses to
// re-pin the per-tenant workspace timezone after CreateCalendarTestTenant
// (which always seeds 'UTC'). The workspace REST surface does not expose
// a tz update for owners yet; until it does, a one-row UPDATE keyed on
// the internal id is the cleanest way to express the "this workspace
// lives in Tokyo" precondition the boundary tests need.
func setWorkspaceTimezone(t *testing.T, db *sql.DB, wsID uint32, tz string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE workspaces SET timezone = ? WHERE id = ?`, tz, wsID)
	require.NoError(t, err, "set workspace timezone")
}

// createCalendarViaAPI creates a personal calendar via POST /calendars on
// the supplied tenant and returns the calendar public id. Matches the
// pattern in apps/flow-api/tests/calendar/calendar_event_test.go so the
// behaviour exercised here is the same one production sees.
func createCalendarViaAPI(t *testing.T, tt *helpers.CalendarTestTenant) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind":  "personal",
		"name":  "Worker Test Calendar " + t.Name(),
		"color": "#4285F4",
	}, &resp)
	require.NotEmpty(t, resp.ID, "calendar create returned empty id")
	return resp.ID
}

// createEventViaAPI creates a calendar_events row via POST
// /calendars/{calId}/events and returns the event public id. The body
// goes through the same Huma validation flow production uses, so the
// row layout (timezone string, start_at unix seconds, all_day flag) is
// guaranteed to match the worker's scanner expectations.
func createEventViaAPI(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	calID string,
	startAt, endAt time.Time,
	tz string,
	title string,
) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  startAt.Unix(),
		"endAt":    endAt.Unix(),
		"timezone": tz,
	}, &resp)
	require.NotEmpty(t, resp.ID, "event create returned empty id")
	return resp.ID
}

// resolveEventInternalID looks up calendar_events.id for a public UUID.
// The signal row stores the internal id in subject_id (rule 18), so the
// assertion needs the same translation the handler does when validating
// the subject reference.
func resolveEventInternalID(t *testing.T, db *sql.DB, eventPublicID string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(context.Background(),
		`SELECT id FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		eventPublicID).Scan(&id)
	require.NoError(t, err, "resolve calendar_events internal id for %s", eventPublicID)
	return id
}

// newJob constructs a calendar_event_day.Job wired against the test
// harness DB and flow-api server. The returned Job uses the injected
// Now func so the boundary tests can pin the wall clock; callers that
// just want "live time" pass nil for now.
func newJob(t *testing.T, db *sql.DB, baseURL string, now func() time.Time) *calendar_event_day.Job {
	t.Helper()
	job, err := calendar_event_day.New(db, baseURL, serviceTokenFixture, "flow-worker/test", silentLogger())
	require.NoError(t, err, "construct calendar_event_day job")
	if now != nil {
		job.Now = now
	}
	return job
}

// signalRowForEvent fetches the single signal row we expect the job to
// have inserted for (workspace_id, event_internal_id). Returns the row
// columns so each test can pin the fields it cares about (external_id,
// payload_json, expires_at, subject_*). Fails the test when zero or
// multiple rows match — the test then knows whether dedupe broke or
// emission silently fanned out.
type signalRow struct {
	ID          int64
	Source      string
	Kind        string
	SubjectType string
	SubjectID   sql.NullInt64
	ExternalID  sql.NullString
	PayloadJSON []byte
	ExpiresAt   sql.NullTime
	ReceivedAt  time.Time
}

func loadSignalsForEvent(t *testing.T, db *sql.DB, wsID uint32, eventInternalID int64) []signalRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT id, source, kind, subject_type, subject_id, external_id, payload_json, expires_at, received_at
		   FROM signals
		  WHERE workspace_id = ?
		    AND subject_type = 'calendar_event'
		    AND subject_id = ?
		  ORDER BY id ASC`,
		wsID, eventInternalID)
	require.NoError(t, err, "query signals for event")
	defer func() { _ = rows.Close() }()

	var out []signalRow
	for rows.Next() {
		var r signalRow
		require.NoError(t, rows.Scan(
			&r.ID, &r.Source, &r.Kind, &r.SubjectType, &r.SubjectID,
			&r.ExternalID, &r.PayloadJSON, &r.ExpiresAt, &r.ReceivedAt,
		))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestEmitsSignalForTodayEvent exercises the happy path: a workspace in
// UTC with one calendar_events row whose start_at falls inside today's
// UTC range. After one Tick the worker must have posted one signal to
// flow-api that lands as an INSERT against the signals table with the
// canonical (source, kind, subject_type, external_id) tuple plus a
// payload JSON containing the event's start instant and all-day flag.
// The Prometheus tick counter status="ok" must increment by exactly 1.
func TestEmitsSignalForTodayEvent(t *testing.T) {
	t.Parallel()
	h := getHarness(t)

	tt := helpers.CreateCalendarTestTenant(t, h.srv)
	// CreateCalendarTestTenant seeds UTC — keep it explicit so a future
	// change to the helper does not silently break this test.
	setWorkspaceTimezone(t, h.db, tt.WorkspaceID, "UTC")

	calID := createCalendarViaAPI(t, tt)

	// Pin "today" in UTC to noon so the start_at falls inside the
	// half-open day range without sitting on the boundary; the worker
	// computes its own range from time.Now, so the event needs to land
	// in whichever UTC day the test machine clock is currently on.
	utcToday := time.Now().UTC().Truncate(24 * time.Hour)
	startAt := utcToday.Add(12 * time.Hour)
	endAt := startAt.Add(time.Hour)
	eventPublicID := createEventViaAPI(t, tt, calID, startAt, endAt, "UTC", "Standup")
	eventInternalID := resolveEventInternalID(t, h.db, eventPublicID)

	tickOKBefore := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("ok"))
	tickErrBefore := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("error"))

	job := newJob(t, h.db, h.srv.BaseURL, nil)
	require.NoError(t, job.Tick(context.Background()), "tick should succeed")

	// The tick counter is a process-wide CounterVec — parallel tests
	// in the same package contend on it. Asserting the lower bound is
	// the safest signal that *this* tick reached the "ok" branch
	// without coupling the test to whatever the other parallel cases
	// just did; the negative assertion below pins that no "error"
	// status leaked in either.
	tickOKDelta := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("ok")) - tickOKBefore
	require.GreaterOrEqualf(t, tickOKDelta, 1.0,
		"tick counter status=ok should increment by at least 1, got delta=%v", tickOKDelta)
	tickErrDelta := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("error")) - tickErrBefore
	require.InDeltaf(t, 0.0, tickErrDelta, 0.0001,
		"tick counter status=error must not move for this happy-path tick, got delta=%v", tickErrDelta)

	signals := loadSignalsForEvent(t, h.db, tt.WorkspaceID, eventInternalID)
	require.Lenf(t, signals, 1, "expected exactly one signal row, got %d", len(signals))

	got := signals[0]
	require.Equal(t, "calendar", got.Source, "source must be the calendar enum value")
	require.Equal(t, "calendar.event_day_arrived", got.Kind, "kind must be the registry value")
	require.Equal(t, "calendar_event", got.SubjectType, "subject_type must be calendar_event")
	require.True(t, got.SubjectID.Valid, "subject_id must be populated")
	require.Equal(t, eventInternalID, got.SubjectID.Int64,
		"subject_id must point at the calendar_events row internal id")

	require.True(t, got.ExternalID.Valid, "external_id must be populated for dedupe")
	expectedDay := utcToday.Format("2006-01-02")
	wantExternalID := "calendar_event_day:" + eventPublicID + ":" + expectedDay
	require.Equal(t, wantExternalID, got.ExternalID.String,
		"external_id must follow calendar_event_day:<event_public_id>:<YYYY-MM-DD>")

	// Payload contract: the worker hands start_at unix seconds and the
	// all_day flag to the judge so the judge does not have to re-fetch
	// the event. Camel-case keys per the calendar_event_day.buildSignalBody
	// docstring.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.PayloadJSON, &payload), "payload_json must be valid JSON")
	startUnix, ok := payload["startAt"].(float64)
	require.Truef(t, ok, "payload.startAt must be a number, got %T", payload["startAt"])
	require.Equal(t, startAt.Unix(), int64(startUnix), "payload.startAt must equal the event start unix")
	allDay, ok := payload["allDay"].(bool)
	require.Truef(t, ok, "payload.allDay must be a bool, got %T", payload["allDay"])
	require.False(t, allDay, "all_day should be false for a regular timed event")

	// expires_at must equal end-of-day UTC (next UTC midnight) within a
	// 1-second tolerance to absorb the wall-clock drift between the
	// test computing utcToday and the worker computing its own Now.
	require.True(t, got.ExpiresAt.Valid, "expires_at must be set")
	wantExpires := utcToday.Add(24 * time.Hour)
	require.WithinDurationf(t, wantExpires, got.ExpiresAt.Time, time.Second,
		"expires_at must equal the UTC end-of-day instant")
}

// TestDedupeOnRetick proves the flow-api INSERT IGNORE on
// (workspace_id, source, external_id) collapses the second tick's
// emission. The worker keeps posting (it has no client-side dedupe);
// the database is the system of record. After two Ticks there must be
// exactly one signal row for the event, and the tick counter must reach
// 2 (both ticks observed and logged as ok).
func TestDedupeOnRetick(t *testing.T) {
	t.Parallel()
	h := getHarness(t)

	tt := helpers.CreateCalendarTestTenant(t, h.srv)
	setWorkspaceTimezone(t, h.db, tt.WorkspaceID, "UTC")
	calID := createCalendarViaAPI(t, tt)

	utcToday := time.Now().UTC().Truncate(24 * time.Hour)
	startAt := utcToday.Add(13 * time.Hour)
	endAt := startAt.Add(time.Hour)
	eventPublicID := createEventViaAPI(t, tt, calID, startAt, endAt, "UTC", "Standup")
	eventInternalID := resolveEventInternalID(t, h.db, eventPublicID)

	tickOKBefore := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("ok"))
	tickErrBefore := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("error"))

	job := newJob(t, h.db, h.srv.BaseURL, nil)
	require.NoError(t, job.Tick(context.Background()), "first tick should succeed")
	require.NoError(t, job.Tick(context.Background()), "second tick should succeed")

	// As in TestEmitsSignalForTodayEvent, the shared CounterVec means
	// only the lower bound on the ok-status delta is safe under
	// t.Parallel(). Two ticks must add at least 2 to ok, and zero to
	// error; if either invariant breaks the regression is real, not
	// a parallel artefact.
	tickOKDelta := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("ok")) - tickOKBefore
	require.GreaterOrEqualf(t, tickOKDelta, 2.0,
		"tick counter status=ok should increment by at least 2 across two ticks, got delta=%v", tickOKDelta)
	tickErrDelta := testutil.ToFloat64(obs.CalendarEventDayTicksTotal.WithLabelValues("error")) - tickErrBefore
	require.InDeltaf(t, 0.0, tickErrDelta, 0.0001,
		"tick counter status=error must not move for two happy-path ticks, got delta=%v", tickErrDelta)

	signals := loadSignalsForEvent(t, h.db, tt.WorkspaceID, eventInternalID)
	require.Lenf(t, signals, 1,
		"two ticks must collapse to exactly one signal row (INSERT IGNORE on the dedupe UNIQUE), got %d", len(signals))
}

// TestWorkspaceTimezoneBoundary pins the per-workspace day boundary
// against the documented Tokyo + UTC pair from the W2 brief. The fixed
// clock instant 2026-05-17T15:30:00Z lands on 2026-05-18 in Tokyo and
// 2026-05-17 in UTC, so each workspace must receive exactly one signal
// whose external_id carries its own local YYYY-MM-DD. A regression in
// localDayUTCRange or eventDayString surfaces here as either a missing
// row or a cross-workspace day label.
func TestWorkspaceTimezoneBoundary(t *testing.T) {
	t.Parallel()
	h := getHarness(t)

	// Pre-flight the tz tables so we fail fast with a clear message on
	// stripped-down test images.
	_ = mustLoadLocation(t, "Asia/Tokyo")

	fixedNow := func() time.Time {
		return time.Date(2026, time.May, 17, 15, 30, 0, 0, time.UTC)
	}

	// Workspace A — Asia/Tokyo. Local "today" is 2026-05-18; the event
	// at 2026-05-18T03:00Z lands at 12:00 Tokyo local on that same day.
	wsA := helpers.CreateCalendarTestTenant(t, h.srv)
	setWorkspaceTimezone(t, h.db, wsA.WorkspaceID, "Asia/Tokyo")
	calA := createCalendarViaAPI(t, wsA)
	startA := time.Date(2026, time.May, 18, 3, 0, 0, 0, time.UTC)
	endA := startA.Add(time.Hour)
	eventA := createEventViaAPI(t, wsA, calA, startA, endA, "Asia/Tokyo", "Tokyo Standup")
	eventAInternalID := resolveEventInternalID(t, h.db, eventA)

	// Workspace B — UTC. Local "today" is 2026-05-17; the event at
	// 2026-05-17T20:00Z lands at 20:00 UTC local on that same day.
	wsB := helpers.CreateCalendarTestTenant(t, h.srv)
	setWorkspaceTimezone(t, h.db, wsB.WorkspaceID, "UTC")
	calB := createCalendarViaAPI(t, wsB)
	startB := time.Date(2026, time.May, 17, 20, 0, 0, 0, time.UTC)
	endB := startB.Add(time.Hour)
	eventB := createEventViaAPI(t, wsB, calB, startB, endB, "UTC", "UTC Standup")
	eventBInternalID := resolveEventInternalID(t, h.db, eventB)

	job := newJob(t, h.db, h.srv.BaseURL, fixedNow)
	require.NoError(t, job.Tick(context.Background()), "tick should succeed")

	// Workspace A → external_id day suffix must be 2026-05-18 (Tokyo).
	signalsA := loadSignalsForEvent(t, h.db, wsA.WorkspaceID, eventAInternalID)
	require.Lenf(t, signalsA, 1, "expected one signal for Tokyo workspace, got %d", len(signalsA))
	require.True(t, signalsA[0].ExternalID.Valid, "ws A external_id must be populated")
	wantExternalA := "calendar_event_day:" + eventA + ":2026-05-18"
	require.Equal(t, wantExternalA, signalsA[0].ExternalID.String,
		"Tokyo workspace external_id must carry the Tokyo-local YYYY-MM-DD (2026-05-18)")

	// Workspace B → external_id day suffix must be 2026-05-17 (UTC).
	signalsB := loadSignalsForEvent(t, h.db, wsB.WorkspaceID, eventBInternalID)
	require.Lenf(t, signalsB, 1, "expected one signal for UTC workspace, got %d", len(signalsB))
	require.True(t, signalsB[0].ExternalID.Valid, "ws B external_id must be populated")
	wantExternalB := "calendar_event_day:" + eventB + ":2026-05-17"
	require.Equal(t, wantExternalB, signalsB[0].ExternalID.String,
		"UTC workspace external_id must carry the UTC-local YYYY-MM-DD (2026-05-17)")
}

// TestExpiresAtIsEndOfDayInWorkspaceTz pins the expires_at column to the
// end of day in the workspace timezone (= start of *next* local
// midnight, projected to UTC). For Asia/Tokyo the local day ends at
// 2026-05-19T00:00 +0900 = 2026-05-18T15:00Z. The retention sweep walks
// expires_at, so a misalignment here would either drop signals early or
// leak them past the day they describe.
func TestExpiresAtIsEndOfDayInWorkspaceTz(t *testing.T) {
	t.Parallel()
	h := getHarness(t)

	_ = mustLoadLocation(t, "Asia/Tokyo")

	fixedNow := func() time.Time {
		return time.Date(2026, time.May, 18, 6, 0, 0, 0, time.UTC) // 15:00 Tokyo local on 2026-05-18
	}

	tt := helpers.CreateCalendarTestTenant(t, h.srv)
	setWorkspaceTimezone(t, h.db, tt.WorkspaceID, "Asia/Tokyo")
	calID := createCalendarViaAPI(t, tt)

	// Event start_at lives inside 2026-05-18 Tokyo-local
	// ([2026-05-17T15:00Z, 2026-05-18T15:00Z)). Noon Tokyo on the 18th
	// is 03:00Z on the 18th — safely inside the range.
	startAt := time.Date(2026, time.May, 18, 3, 0, 0, 0, time.UTC)
	endAt := startAt.Add(time.Hour)
	eventPublicID := createEventViaAPI(t, tt, calID, startAt, endAt, "Asia/Tokyo", "Tokyo Standup")
	eventInternalID := resolveEventInternalID(t, h.db, eventPublicID)

	job := newJob(t, h.db, h.srv.BaseURL, fixedNow)
	require.NoError(t, job.Tick(context.Background()), "tick should succeed")

	signals := loadSignalsForEvent(t, h.db, tt.WorkspaceID, eventInternalID)
	require.Lenf(t, signals, 1, "expected exactly one signal row, got %d", len(signals))

	row := signals[0]
	require.True(t, row.ExpiresAt.Valid, "expires_at must be set")
	wantExpires := time.Date(2026, time.May, 18, 15, 0, 0, 0, time.UTC)
	require.WithinDurationf(t, wantExpires, row.ExpiresAt.Time, time.Second,
		"expires_at must equal Tokyo-local end-of-day (2026-05-19 00:00 +0900 = 2026-05-18 15:00Z); got %s",
		row.ExpiresAt.Time.UTC().Format(time.RFC3339))
}
