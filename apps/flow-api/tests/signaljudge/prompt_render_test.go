// signal_judge prompt rendering integration tests (Phase 6 / L1).
//
// These tests are the end-to-end deterministic snapshot of the full
// rendered judge prompt against a real testcontainer MySQL. They wire
// the three [signaljudge.PromptDeps] lookups against the live schema
// via inline SQL adapters (the unit tests in
// apps/flow-api/internal/ai/signaljudge/prompt_test.go cover the cap
// math with fakes; this file exercises the SQL specifically).
//
// Production wiring of the lookups in
// apps/flow-api/cmd/api/main.go is NOT yet in place: the judgeRunner
// constructor leaves [signaljudge.Runner.Prompt] zero-valued, which
// causes [signaljudge.Runner.renderUserPrompt] to fall back to the
// Phase 2 [signaljudge.composeJudgePrompt] shape. Until the runner is
// wired with these adapters, the rendered prompt these tests assert is
// not exercised on the live path. The adapter shapes here mirror what
// the production wiring will need so the gap is mechanical to close.
package signaljudgetests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// ----- Inline SQL adapters --------------------------------------------------
//
// These mirror the shape the production wiring at
// apps/flow-api/cmd/api/main.go will eventually use. They are
// deliberately small SELECTs against the live schema so the test
// exercises the real SQL contract (column types, NULL handling,
// ordering) instead of stubbing it out.

// sqlRecentTasksLookup loads the most recent tasks for a workspace
// ordered by created_at DESC (newest first). The order is pinned so
// the rendered prompt is byte-stable across re-runs.
type sqlRecentTasksLookup struct{ db *sql.DB }

// LoadRecent implements [signaljudge.RecentTasksLookup].
func (l *sqlRecentTasksLookup) LoadRecent(ctx context.Context, workspaceID uint32, limit int) ([]signaljudge.TaskSummary, error) {
	const q = `
		SELECT BIN_TO_UUID(public_id, 0), title, derived_state, due_on
		FROM tasks
		WHERE workspace_id = ? AND enabled = TRUE
		ORDER BY created_at DESC, id DESC
		LIMIT ?`
	rows, err := l.db.QueryContext(ctx, q, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []signaljudge.TaskSummary{}
	for rows.Next() {
		var pub, title, state string
		var due sql.NullTime
		if err := rows.Scan(&pub, &title, &state, &due); err != nil {
			return nil, err
		}
		ts := signaljudge.TaskSummary{
			PublicID:     pub,
			Title:        title,
			DerivedState: state,
		}
		if due.Valid {
			ts.DueOn = due.Time.UTC().Format("2006-01-02")
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// sqlLinkedTasksLookup loads tasks attached to a calendar_event subject
// via task_event_links (the M:N table). Mirrors the JOIN shape of
// queries.ListLinkedTasksForEvent without the windowed COUNT — the
// prompt builder caps the result independently.
type sqlLinkedTasksLookup struct{ db *sql.DB }

// LoadLinked implements [signaljudge.LinkedTasksLookup]. The
// eventInternalID matches signals.subject_id when SubjectType is
// calendar_event.
func (l *sqlLinkedTasksLookup) LoadLinked(ctx context.Context, workspaceID uint32, eventInternalID int32, limit int) ([]signaljudge.TaskSummary, error) {
	const q = `
		SELECT BIN_TO_UUID(t.public_id, 0), t.title, t.derived_state, t.due_on
		FROM task_event_links tel
		INNER JOIN tasks t ON t.id = tel.task_id AND t.enabled = TRUE
		WHERE tel.workspace_id = ? AND tel.event_id = ? AND tel.enabled = TRUE
		ORDER BY tel.sort_weight ASC, tel.created_at ASC, tel.id ASC
		LIMIT ?`
	rows, err := l.db.QueryContext(ctx, q, workspaceID, eventInternalID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []signaljudge.TaskSummary{}
	for rows.Next() {
		var pub, title, state string
		var due sql.NullTime
		if err := rows.Scan(&pub, &title, &state, &due); err != nil {
			return nil, err
		}
		ts := signaljudge.TaskSummary{
			PublicID:     pub,
			Title:        title,
			DerivedState: state,
		}
		if due.Valid {
			ts.DueOn = due.Time.UTC().Format("2006-01-02")
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// sqlJudgeInstructionsLookup reads ai_settings.judge_instructions for
// the workspace. An absent row returns "" (no per-workspace policy);
// a NULL judge_instructions column also returns "".
type sqlJudgeInstructionsLookup struct{ db *sql.DB }

// LoadInstructions implements [signaljudge.JudgeInstructionsLookup].
func (l *sqlJudgeInstructionsLookup) LoadInstructions(ctx context.Context, workspaceID uint32) (string, error) {
	const q = `SELECT judge_instructions FROM ai_settings WHERE workspace_id = ? LIMIT 1`
	var raw sql.NullString
	err := l.db.QueryRowContext(ctx, q, workspaceID).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !raw.Valid {
		return "", nil
	}
	return raw.String, nil
}

// ----- Test fixtures --------------------------------------------------------

// resolveWorkspaceInternalID resolves a workspace public id to its
// internal row id. Mirrors the helper in tests/e2e but inlined so this
// package is self-contained.
func resolveWorkspaceInternalID(t *testing.T, db *sql.DB, workspacePublicID string) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&id))
	require.NotZero(t, id, "workspace id resolution returned zero")
	return id
}

// resolveCalendarEventInternalID resolves a calendar_events public id
// to its internal row id within the supplied workspace.
func resolveCalendarEventInternalID(t *testing.T, db *sql.DB, workspaceID uint32, eventPublicID string) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id int32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM calendar_events WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, eventPublicID,
	).Scan(&id))
	require.NotZero(t, id, "calendar event id resolution returned zero")
	return id
}

// resolveTaskInternalID resolves a tasks public id to its internal id
// within the supplied workspace.
func resolveTaskInternalID(t *testing.T, db *sql.DB, workspaceID uint32, taskPublicID string) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, taskPublicID,
	).Scan(&id))
	require.NotZero(t, id, "task id resolution returned zero")
	return id
}

// seedTaskViaAPI POSTs a task through the live HTTP API and returns
// the public id. Using the API rather than INSERT keeps the test
// faithful to the integration contract (no DB writes from test code
// for entities that have a creation endpoint).
func seedTaskViaAPI(t *testing.T, tt *helpers.TestTenant, title string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     title,
	}
	req := mustNewJSONRequest(t, http.MethodPost, tt.BaseURL+"/tasks", tt.AccessToken, body)
	mustDoJSON(t, req, &out)
	require.NotEmpty(t, out.ID, "POST /tasks did not return id for %q", title)
	return out.ID
}

// seedCalendarViaAPI creates a personal calendar and returns the
// public id.
func seedCalendarViaAPI(t *testing.T, tt *helpers.TestTenant, name string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"kind":  "personal",
		"name":  name,
		"color": "#4285F4",
	}
	req := mustNewJSONRequest(t, http.MethodPost,
		tt.BaseURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars",
		tt.AccessToken, body)
	mustDoJSON(t, req, &out)
	require.NotEmpty(t, out.ID, "POST /calendars did not return id for %q", name)
	return out.ID
}

// seedCalendarEventViaAPI creates an event on the given calendar and
// returns its public id.
func seedCalendarEventViaAPI(t *testing.T, tt *helpers.TestTenant, calendarID, title string, startAt, endAt time.Time) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  startAt.Unix(),
		"endAt":    endAt.Unix(),
		"timezone": "UTC",
	}
	req := mustNewJSONRequest(t, http.MethodPost,
		tt.BaseURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calendarID+"/events",
		tt.AccessToken, body)
	mustDoJSON(t, req, &out)
	require.NotEmpty(t, out.ID, "POST /calendars/{id}/events did not return id for %q", title)
	return out.ID
}

// linkTaskToEventViaAPI creates a task_event_links row via POST
// /tasks/{id}/links with relation=contributes_to.
func linkTaskToEventViaAPI(t *testing.T, tt *helpers.TestTenant, taskPublicID, eventPublicID string) {
	t.Helper()
	body := map[string]any{
		"eventId":  eventPublicID,
		"relation": "contributes_to",
	}
	req := mustNewJSONRequest(t, http.MethodPost,
		tt.BaseURL+"/tasks/"+taskPublicID+"/links",
		tt.AccessToken, body)
	mustDoJSON(t, req, nil)
}

// setDerivedStateDirect overrides tasks.derived_state via raw SQL.
// Production code never updates derived_state directly (events are the
// sole writer per CLAUDE.md rule #10), but the prompt-render test only
// cares that the projected state surfaces in the prompt, not how it
// got there. Scoping this to the test keeps it from leaking into
// production helpers.
func setDerivedStateDirect(t *testing.T, db *sql.DB, workspaceID, taskID uint32, state string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx,
		`UPDATE tasks SET derived_state = ? WHERE workspace_id = ? AND id = ?`,
		state, workspaceID, taskID,
	)
	require.NoError(t, err)
}

// upsertJudgeInstructions writes ai_settings.judge_instructions for the
// workspace via raw SQL. There is no public REST endpoint for this
// field yet, so direct SQL is the only path; this is consistent with
// tests/e2e/autoaction_executor_test.go's setWorkspaceAutoActionThreshold.
func upsertJudgeInstructions(t *testing.T, db *sql.DB, workspaceID uint32, instructions string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx,
		`INSERT INTO ai_settings (workspace_id, judge_instructions)
		 VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE judge_instructions = VALUES(judge_instructions)`,
		workspaceID, instructions,
	)
	require.NoError(t, err)
}

// insertCalendarEventSignal inserts a signals row with
// subject_type='calendar_event' and subject_id pointing at the supplied
// event internal id. Returns the SignalSnapshot the test feeds to
// BuildPromptContext (with a JSON-encoded payload exactly as the
// production signal lookup would have produced).
func insertCalendarEventSignal(t *testing.T, db *sql.DB, workspaceID uint32, eventInternalID int32, kind string, payload map[string]any) signaljudge.SignalSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	res, err := db.ExecContext(ctx, `
		INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type, subject_id)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, ?, NOW(3), 'calendar_event', ?)`,
		workspaceID, "calendar", kind, string(payloadBytes), eventInternalID,
	)
	require.NoError(t, err)
	signalID, err := res.LastInsertId()
	require.NoError(t, err)

	return readSignalSnapshot(t, db, signalID)
}

// insertWorkspaceSignal inserts a manual signal with
// subject_type='workspace' (no subject_id) and returns the snapshot.
func insertWorkspaceSignal(t *testing.T, db *sql.DB, workspaceID uint32, kind string, payload map[string]any) signaljudge.SignalSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	res, err := db.ExecContext(ctx, `
		INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, 'manual', ?, ?, NOW(3), 'workspace')`,
		workspaceID, kind, string(payloadBytes),
	)
	require.NoError(t, err)
	signalID, err := res.LastInsertId()
	require.NoError(t, err)
	return readSignalSnapshot(t, db, signalID)
}

// readSignalSnapshot fetches a signals row by internal id and
// projects it into a SignalSnapshot identical to what the production
// SQLSignalLookup would return. The UNIX_TIMESTAMP arithmetic is
// wrapped in CAST(... AS SIGNED) because received_at is DATETIME(3);
// the implicit DECIMAL(16,3) MySQL emits otherwise scans into
// []uint8 rather than int64 (driver-side type pinning).
func readSignalSnapshot(t *testing.T, db *sql.DB, signalID int64) signaljudge.SignalSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var snap signaljudge.SignalSnapshot
	var subjectID sql.NullInt32
	err := db.QueryRowContext(ctx, `
		SELECT id, BIN_TO_UUID(public_id, 0), workspace_id, source, kind, subject_type, subject_id, payload_json,
		       CAST(UNIX_TIMESTAMP(received_at) * 1000 AS SIGNED)
		FROM signals WHERE id = ? LIMIT 1`,
		signalID,
	).Scan(
		&snap.SignalID, &snap.PublicID, &snap.WorkspaceID, &snap.Source, &snap.Kind,
		&snap.SubjectType, &subjectID, &snap.PayloadJSON, &snap.ReceivedAtMs,
	)
	require.NoError(t, err)
	snap.SubjectID = subjectID
	return snap
}

// fixedNowDeps returns a [signaljudge.PromptDeps] whose WorkspaceNow
// hook is pinned to a fixed RFC3339 timestamp. The fixed-clock lets
// the deterministic-rendering test assert byte equality between two
// renders without flake from time.Now().
func fixedNowDeps(db *sql.DB) signaljudge.PromptDeps {
	return signaljudge.PromptDeps{
		RecentTasks:       &sqlRecentTasksLookup{db: db},
		LinkedTasks:       &sqlLinkedTasksLookup{db: db},
		JudgeInstructions: &sqlJudgeInstructionsLookup{db: db},
		WorkspaceNow: func(_ context.Context, _ uint32) (string, error) {
			return "2026-05-17T12:00:00Z", nil
		},
	}
}

// ----- HTTP request helpers ------------------------------------------------
//
// Stand-alone copies of the JSON helpers in tests/e2e/main_test.go so
// this package does not import its sibling test package.

func mustNewJSONRequest(t *testing.T, method, url, bearer string, body any) *http.Request {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = strings.NewReader(string(buf))
	}
	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequest(method, url, rdr)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

func mustDoJSON(t *testing.T, req *http.Request, out any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "%s %s", req.Method, req.URL.String())
	defer func() { _ = resp.Body.Close() }()
	var raw []byte
	raw, err = readAll(resp.Body)
	require.NoError(t, err)
	require.GreaterOrEqualf(t, resp.StatusCode, 200, "%s %s -> %d body=%s", req.Method, req.URL.String(), resp.StatusCode, string(raw))
	require.Lessf(t, resp.StatusCode, 300, "%s %s -> %d body=%s", req.Method, req.URL.String(), resp.StatusCode, string(raw))
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out))
	}
}

// readAll drains an io.ReadCloser into a byte slice without pulling in
// the io package alias dance (helps the test file stay self-contained).
func readAll(r interface {
	Read(p []byte) (int, error)
}) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}

// ----- Test 1: deterministic rendering --------------------------------------

// TestRenderedPromptIsDeterministic seeds a fully-populated workspace
// (timezone=UTC, 5 tasks with mixed derived_state, 1 calendar event
// with 2 linked tasks, 1 calendar_event signal, judge_instructions
// configured) and asserts that two back-to-back renders of the same
// PromptContext produce byte-identical output. Catches map iteration
// leaks, time.Now() leakage, or any other non-deterministic write into
// the rendered string.
//
// Also asserts the rendered prompt contains every input we seeded so
// the cap math (capTasks / capJudgeInstructions) cannot accidentally
// drop a row when the corpus is well under the limits.
func TestRenderedPromptIsDeterministic(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)

	// 1. Pin workspace timezone to UTC so the test does not depend on
	//    region.DefaultTimezone. The CreateTestTenant flow sets the
	//    timezone via region default; we force UTC for hermeticity.
	_, err := testDB.ExecContext(ctx,
		`UPDATE workspaces SET timezone = 'UTC' WHERE id = ?`, wsID)
	require.NoError(t, err)

	// 2. judge_instructions: a specific, recognisable string that must
	//    survive verbatim into the rendered prompt.
	const judgeInstructions = "Prefer drafts for daily standups; treat retros as low priority."
	upsertJudgeInstructions(t, testDB, wsID, judgeInstructions)

	// 3. Five tasks with mixed derived_state values. Titles are
	//    distinct so the assertions can grep for each.
	taskTitles := []string{
		"determ-render-task-alpha",
		"determ-render-task-bravo",
		"determ-render-task-charlie",
		"determ-render-task-delta",
		"determ-render-task-echo",
	}
	taskStates := []string{"open", "open", "done", "waiting", "done"}
	taskPubIDs := make([]string, len(taskTitles))
	for i, title := range taskTitles {
		taskPubIDs[i] = seedTaskViaAPI(t, tt, title)
		internalID := resolveTaskInternalID(t, testDB, wsID, taskPubIDs[i])
		setDerivedStateDirect(t, testDB, wsID, internalID, taskStates[i])
	}

	// 4. One calendar event with two linked tasks.
	calID := seedCalendarViaAPI(t, tt, "Determ Cal")
	startAt := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	endAt := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	eventPub := seedCalendarEventViaAPI(t, tt, calID, "determ-render-event-standup", startAt, endAt)
	eventInternalID := resolveCalendarEventInternalID(t, testDB, wsID, eventPub)

	// Link the first two task seeds to the event so RecentTasks and
	// LinkedTasks both have non-empty content.
	linkTaskToEventViaAPI(t, tt, taskPubIDs[0], eventPub)
	linkTaskToEventViaAPI(t, tt, taskPubIDs[1], eventPub)

	// 5. One signal pointing at the event subject. Kind matches the
	//    production "calendar.event_day_arrived" string the daily
	//    scheduler emits; payload carries start_at + all_day so the
	//    judge can reason about timing.
	const signalKind = "calendar.event_day_arrived"
	signal := insertCalendarEventSignal(t, testDB, wsID, eventInternalID, signalKind, map[string]any{
		"start_at": startAt.Unix(),
		"all_day":  false,
		"event_id": eventPub,
	})

	// Build the prompt twice with the same deps + same signal and
	// assert byte equality. The fixed clock is essential: a render with
	// time.Now() would produce a different "Now" line every call.
	deps := fixedNowDeps(testDB)

	pc1, err := signaljudge.BuildPromptContext(ctx, deps, signal)
	require.NoError(t, err)
	pc2, err := signaljudge.BuildPromptContext(ctx, deps, signal)
	require.NoError(t, err)
	render1 := signaljudge.RenderUserPrompt(pc1)
	render2 := signaljudge.RenderUserPrompt(pc2)
	require.Equal(t, render1, render2,
		"renders must be byte-identical for the same input; map iteration leak suspected")

	// Content assertions on render1.
	require.Contains(t, render1, signalKind, "rendered prompt must include signal kind")
	require.Contains(t, render1, judgeInstructions, "judge_instructions text must appear verbatim")

	// Both linked task titles must surface.
	require.Contains(t, render1, taskTitles[0], "first linked task title missing from prompt")
	require.Contains(t, render1, taskTitles[1], "second linked task title missing from prompt")

	// All 5 recent task titles must surface (we are well under
	// MaxRecentTasks=20).
	for _, title := range taskTitles {
		require.Contains(t, render1, title,
			"recent task title missing from prompt: %s", title)
	}

	// Section headers must all be present.
	require.Contains(t, render1, "## Signal", "Signal section header missing")
	require.Contains(t, render1, "## Recent tasks", "Recent tasks section header missing")
	require.Contains(t, render1, "## Linked tasks", "Linked tasks section header missing")
	require.Contains(t, render1, "## Judge instructions", "Judge instructions section header missing")
	require.Contains(t, render1, "## Now", "Now section header missing")
	require.Contains(t, render1, "2026-05-17T12:00:00Z", "fixed-clock Now value missing")
}

// ----- Test 2: cap enforcement ---------------------------------------------

// TestRenderedPromptCapsContextSize seeds 50 recent tasks and 30
// linked tasks (well past the per-section caps) and asserts the
// rendered prompt enforces every limit: total bytes <= MaxContextBytes,
// recent tasks bullet count <= MaxRecentTasks, linked tasks bullet
// count <= MaxLinkedTasks, and the Signal block is never dropped.
func TestRenderedPromptCapsContextSize(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)

	// 50 recent tasks. The titles are kept short so even at 50 rows
	// the corpus alone does not push us past MaxContextBytes — the
	// test is asserting the cap math, not byte-budget truncation.
	const totalTasks = 50
	taskPubIDs := make([]string, totalTasks)
	for i := 0; i < totalTasks; i++ {
		taskPubIDs[i] = seedTaskViaAPI(t, tt, fmt.Sprintf("cap-test-task-%03d", i))
	}

	// One calendar event + 30 linked task rows.
	calID := seedCalendarViaAPI(t, tt, "Cap Cal")
	startAt := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	endAt := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	eventPub := seedCalendarEventViaAPI(t, tt, calID, "cap-test-event", startAt, endAt)
	eventInternalID := resolveCalendarEventInternalID(t, testDB, wsID, eventPub)

	const totalLinks = 30
	for i := 0; i < totalLinks; i++ {
		linkTaskToEventViaAPI(t, tt, taskPubIDs[i], eventPub)
	}

	// Inject a signal pointing at the event.
	signal := insertCalendarEventSignal(t, testDB, wsID, eventInternalID,
		"calendar.event_day_arrived", map[string]any{"event_id": eventPub})

	deps := fixedNowDeps(testDB)
	pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
	require.NoError(t, err)
	rendered := signaljudge.RenderUserPrompt(pc)

	// Byte budget: the entire rendered body must fit under
	// MaxContextBytes (12 KB). With 50 short task titles + 30 linked
	// short titles the natural total is well under, but the assertion
	// also catches a future regression where the renderer adds an
	// expensive new section without updating the truncator.
	require.LessOrEqual(t, len(rendered), signaljudge.MaxContextBytes,
		"rendered prompt exceeded MaxContextBytes (got %d, want <= %d)",
		len(rendered), signaljudge.MaxContextBytes)

	// PromptContext caps: the builder must clip the lookup results to
	// the documented section limits before render.
	require.LessOrEqual(t, len(pc.RecentTasks), signaljudge.MaxRecentTasks,
		"RecentTasks must be capped at MaxRecentTasks")
	require.Equal(t, signaljudge.MaxRecentTasks, len(pc.RecentTasks),
		"with %d seeded tasks RecentTasks must reach MaxRecentTasks", totalTasks)
	require.LessOrEqual(t, len(pc.LinkedTasks), signaljudge.MaxLinkedTasks,
		"LinkedTasks must be capped at MaxLinkedTasks")
	require.Equal(t, signaljudge.MaxLinkedTasks, len(pc.LinkedTasks),
		"with %d seeded links LinkedTasks must reach MaxLinkedTasks", totalLinks)

	// Recent tasks bullets in the rendered body: count "\n- " in the
	// Recent tasks section only. Strictly speaking strings.Count("\n- ",
	// rendered) would also catch the Linked tasks bullets, so slice
	// the section by header positions.
	recentSection := extractSection(t, rendered, "## Recent tasks", "## Linked tasks")
	require.LessOrEqual(t, countBullets(recentSection), signaljudge.MaxRecentTasks,
		"Recent tasks section enumerates more than %d bullets", signaljudge.MaxRecentTasks)

	linkedSection := extractSection(t, rendered, "## Linked tasks", "## Judge instructions")
	if linkedSection == "" {
		// Judge instructions section is missing when ai_settings
		// has no row for this workspace, which is the default in this
		// test; fall back to the Now header as the terminator.
		linkedSection = extractSection(t, rendered, "## Linked tasks", "## Now")
	}
	require.LessOrEqual(t, countBullets(linkedSection), signaljudge.MaxLinkedTasks,
		"Linked tasks section enumerates more than %d bullets", signaljudge.MaxLinkedTasks)

	// Signal block invariant: the renderer must never drop the Signal
	// section during truncation, otherwise the judge has nothing to
	// judge.
	require.Contains(t, rendered, "## Signal", "Signal section dropped during truncation")
	require.Contains(t, rendered, "calendar.event_day_arrived", "signal kind missing post-render")
}

// extractSection returns the substring of rendered between the start
// and end headers (exclusive of end). Returns "" when start is not
// present. Used to scope bullet counts to a specific section.
func extractSection(t *testing.T, rendered, startHeader, endHeader string) string {
	t.Helper()
	startIdx := strings.Index(rendered, startHeader)
	if startIdx < 0 {
		return ""
	}
	tail := rendered[startIdx:]
	endIdx := strings.Index(tail[len(startHeader):], endHeader)
	if endIdx < 0 {
		return tail
	}
	return tail[:len(startHeader)+endIdx]
}

// countBullets counts lines that start with "- " in the supplied
// section text. Pinned to the markdown bullet shape the renderer emits
// (see writeTaskList in prompt.go).
func countBullets(section string) int {
	count := 0
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "- ") {
			count++
		}
	}
	return count
}

// ----- Test 3: secret redaction --------------------------------------------

// TestRenderedPromptRedactsSecrets seeds both
// ai_settings.judge_instructions and the signal payload with strings
// that the prompt-side redactor recognises (Bearer tokens, ghp_*
// GitHub PATs, access_token / refresh_token JSON keys). Asserts the
// rendered prompt contains [REDACTED] markers and does NOT contain any
// of the secret values.
//
// This is the programmatic gate the L1 plan DoD calls for ("secret 0
// 件" via the audit-secrets skill conceptually).
func TestRenderedPromptRedactsSecrets(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)

	// 1. judge_instructions with two free-form secrets:
	//    - "Bearer xoxb-..." matches Bearer + xoxb- prefixes
	//    - "ghp_..." matches ghp_ prefix
	const leakedSlack = "xoxb-fake-slack-token-here"
	const leakedGithub = "ghp_realgithubtokenshouldbestripped"
	instructions := fmt.Sprintf(
		"Use Bearer %s for fallback. %s should also be hidden.",
		leakedSlack, leakedGithub,
	)
	upsertJudgeInstructions(t, testDB, wsID, instructions)

	// 2. signal payload with access_token + refresh_token JSON keys
	//    (both in jsonRedactKeys) plus a benign value ("alice").
	const leakedAccess = "should-be-redacted-access"
	const leakedRefresh = "also-redacted-refresh"
	signal := insertWorkspaceSignal(t, testDB, wsID, "manual", map[string]any{
		"access_token":  leakedAccess,
		"refresh_token": leakedRefresh,
		"user":          "alice",
	})

	deps := fixedNowDeps(testDB)
	pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
	require.NoError(t, err)
	rendered := signaljudge.RenderUserPrompt(pc)

	// Sanity: the rendered prompt must contain the [REDACTED] marker
	// for at least the two JSON keys (access_token, refresh_token) and
	// the two free-form matches (Bearer xoxb-..., ghp_...).
	const marker = "[REDACTED]"
	markerCount := strings.Count(rendered, marker)
	require.GreaterOrEqual(t, markerCount, 4,
		"expected at least 4 [REDACTED] markers (2 JSON keys + 2 free-form), got %d in:\n%s",
		markerCount, rendered)

	// None of the secret values may leak.
	require.NotContains(t, rendered, leakedSlack,
		"xoxb-... slack token leaked into rendered prompt")
	require.NotContains(t, rendered, leakedGithub,
		"ghp_... github token leaked into rendered prompt")
	require.NotContains(t, rendered, leakedAccess,
		"access_token value leaked into rendered prompt")
	require.NotContains(t, rendered, leakedRefresh,
		"refresh_token value leaked into rendered prompt")

	// Benign payload values must survive — over-redaction is also a
	// quality problem (the LLM needs context).
	require.Contains(t, rendered, "alice",
		"benign payload value (user=alice) was unexpectedly redacted")
}

// ----- Test 4: empty context -----------------------------------------------

// TestRenderedPromptHandlesEmptyContext seeds a fresh workspace with
// no tasks, no events, no judge_instructions, and feeds a workspace-
// subject manual signal. The renderer must not panic and must still
// produce a non-empty prompt body (at least the Signal block + Now
// header). The omitted sections must NOT appear at all — the renderer
// skips empty sections rather than printing a "No recent tasks"
// placeholder (see prompt.go renderFull, which gates each section on
// the slice / string being non-empty).
func TestRenderedPromptHandlesEmptyContext(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := resolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)

	// No tasks. No events. No ai_settings row. No judge_instructions.
	// Just a manual signal scoped to the workspace subject.
	signal := insertWorkspaceSignal(t, testDB, wsID, "manual", map[string]any{
		"empty_context_smoke": true,
	})

	deps := fixedNowDeps(testDB)
	pc, err := signaljudge.BuildPromptContext(ctx, deps, signal)
	require.NoError(t, err)

	// PromptContext shape: every optional section must be empty.
	require.Empty(t, pc.RecentTasks, "RecentTasks must be empty when no tasks exist")
	require.Empty(t, pc.LinkedTasks, "LinkedTasks must be empty when no event/links exist")
	require.Empty(t, pc.JudgeInstructions, "JudgeInstructions must be empty when ai_settings row is absent")

	// Render must not panic and must produce non-empty output.
	var rendered string
	require.NotPanics(t, func() { rendered = signaljudge.RenderUserPrompt(pc) },
		"RenderUserPrompt must not panic on empty context")
	require.NotEmpty(t, rendered, "rendered prompt must be non-empty even with no tasks")

	// Signal section + Now section are mandatory.
	require.Contains(t, rendered, "## Signal", "Signal section missing on empty context")
	require.Contains(t, rendered, "## Now", "Now section missing on empty context")
	require.Contains(t, rendered, "manual", "signal kind missing on empty context")

	// Optional sections must NOT appear (the renderer skips empty
	// sections rather than printing a placeholder).
	require.NotContains(t, rendered, "## Recent tasks",
		"Recent tasks header must be absent when there are no tasks")
	require.NotContains(t, rendered, "## Linked tasks",
		"Linked tasks header must be absent for workspace-subject signals")
	require.NotContains(t, rendered, "## Judge instructions",
		"Judge instructions header must be absent when ai_settings has no row")
}
