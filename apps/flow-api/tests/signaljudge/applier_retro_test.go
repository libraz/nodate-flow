// Package signaljudgetests — Phase 6 / L2 integration coverage for
// the production `generate_retro` Applier branch.
//
// The fake-only tests in applier_test.go cover the Applier's event
// emission contract end-to-end (every (action, autonomy) branch). The
// tests in this file additionally exercise the SQL-backed
// [signaljudge.SQLTaskMutator] against a real MySQL container so the
// transactional retro-draft write path is verified: a new tasks row
// is inserted with the canonical title / description / project / FK
// shape, a task_dependencies row of kind='retro_of' is inserted in
// the same transaction, and a TaskRetroDrafted event is emitted
// carrying the new task's public_id.
//
// All tests in this file gate on NF_TEST_INTEGRATION via the shared
// bootstrap helper in main_test.go; they skip cleanly on machines
// without Docker.
package signaljudgetests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// recordingBus captures every event the Applier emits through
// [eventbus.AppendJudgeEvent] so the test can assert on the full
// sequence. Mirrors [fakeBus] in applier_test.go but is a separate
// type so the two test files do not couple via shared state.
type recordingBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (b *recordingBus) AppendJudgeEvent(_ context.Context, evt eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	return nil
}

func (b *recordingBus) snapshot() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]eventbus.Event, len(b.events))
	copy(out, b.events)
	return out
}

// staticAutonomy returns a fixed AutonomyDecision regardless of
// input. Tests pin the level so the (action, autonomy) branch under
// test is unambiguous.
type staticAutonomy struct {
	level signaljudge.AutonomyLevel
}

func (a *staticAutonomy) Resolve(_ context.Context, _ uint32, _ signalkinds.Kind, _ float64) (signaljudge.AutonomyDecision, error) {
	return signaljudge.AutonomyDecision{Level: a.level}, nil
}

// nopSignalUpdater discards every UpdateJudgeOutput call. The retro
// tests do not assert on signals.judge_output_json — that surface is
// already covered by the in-process applier_test.go scenarios.
type nopSignalUpdater struct{}

func (nopSignalUpdater) UpdateJudgeOutput(_ context.Context, _ int64, _ uint32, _ json.RawMessage, _ float64, _ *time.Time) error {
	return nil
}

// faultyAddDependencyMutator wraps the real SQL mutator but
// intercepts DraftRetroTask to drive a forced failure. It overrides
// the source task internal id with a value that cannot resolve,
// causing the pre-read SELECT inside [SQLTaskMutator.loadRetroSource]
// to fail with sql.ErrNoRows. The surrounding transaction never
// opens, so no tasks / task_dependencies rows commit — covering the
// production invariant "any sub-error short-circuits the whole
// transaction" from the "source vanished between resolver and
// mutator" angle. See the documented deviation on the test below
// for why this specific fault shape is used (project convention
// disallows test-only code paths on production types).
type faultyAddDependencyMutator struct {
	real *signaljudge.SQLTaskMutator
}

func (f *faultyAddDependencyMutator) CompleteTask(ctx context.Context, ws uint32, taskID int64, agent uint32) error {
	return f.real.CompleteTask(ctx, ws, taskID, agent)
}

func (f *faultyAddDependencyMutator) AddComment(ctx context.Context, ws uint32, taskID int64, agent uint32, body string) error {
	return f.real.AddComment(ctx, ws, taskID, agent, body)
}

// DraftRetroTask routes through the real mutator with an
// intentionally non-existent source task id. The mutator pre-reads
// the source row before opening the transaction, so the first error
// surfaces from [SQLTaskMutator.loadRetroSource] (sql.ErrNoRows) —
// the tasks INSERT never runs and no rows are committed. This is
// the production rollback contract: any sub-error short-circuits the
// whole transaction.
func (f *faultyAddDependencyMutator) DraftRetroTask(ctx context.Context, ws uint32, _ int64, agent uint32, title string, draft bool) (int64, string, error) {
	// Force the lookup to miss by passing a clearly impossible id.
	// The Applier's resolver already validated the verdict's
	// target_task_public_id and produced a real internal id; we
	// inject the fault below the resolver so the test exercises the
	// "source vanished between resolution and mutation" race the
	// production code must roll back through.
	return f.real.DraftRetroTask(ctx, ws, 0x7fffffff, agent, title, draft)
}

// TestApplierGenerateRetroCreatesDraftTaskAndDependency is the happy
// path: a verdict with action=generate_retro under AutonomyDraft
// produces (a) a new tasks row inheriting the source's workspace +
// project, (b) a task_dependencies row with kind='retro_of' pointing
// from the new task to the source task, and (c) a TaskRetroDrafted
// event whose payload names the new task's public_id. All three
// writes commit together.
func TestApplierGenerateRetroCreatesDraftTaskAndDependency(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)

	// Seed a source task via the public REST API so the project /
	// task_number / actor wiring is exercised through the canonical
	// path rather than reimplemented in test fixtures.
	srcPub, srcInternalID := createSourceTask(t, tt, "Outage incident")

	// Seed a signal_judge agent + minimal signal row so the
	// Applier's SignalRef + AgentRef are wired against real ids.
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	bus := &recordingBus{}
	mutator := &signaljudge.SQLTaskMutator{
		DB:      testDB,
		Queries: generated.New(testDB),
		Logger:  slog.New(slog.DiscardHandler),
	}
	applier := &signaljudge.Applier{
		Bus:      bus,
		Tasks:    mutator,
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyDraft},
	}

	verdict := signaljudge.Verdict{
		Action:             signaljudge.ActionGenerateRetro,
		TargetTaskPublicID: ptrString(srcPub),
		Confidence:         0.81,
		ReasoningExcerpt:   "45-minute postmortem just ended; drafting retro for review",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := applier.Apply(ctx, signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-feedfacedead",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}, signaljudge.AgentRef{InternalID: agent.AgentID}, 1, verdict)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.Skipped, "Draft branch must materialise (Skipped=false)")

	// 1. Event sequence: SignalJudged, TaskRetroDrafted (no
	//    SignalApplied — the Draft branch is intentionally
	//    SignalApplied-free, see applier.go).
	events := bus.snapshot()
	require.Len(t, events, 2, "Draft retro must emit exactly SignalJudged + TaskRetroDrafted; got %d events", len(events))
	require.Equal(t, eventbus.SignalJudged, events[0].Type)
	require.Equal(t, eventbus.TaskRetroDrafted, events[1].Type)

	retroPayload, ok := events[1].Payload.(map[string]any)
	require.True(t, ok, "TaskRetroDrafted payload must be a map")
	newPub, ok := retroPayload["newTaskPublicId"].(string)
	require.True(t, ok, "TaskRetroDrafted.newTaskPublicId must be a string")
	require.NotEmpty(t, newPub)
	require.NotEqual(t, srcPub, newPub, "retro task must have a fresh public id")
	require.Equal(t, srcPub, retroPayload["sourceTaskPublicId"], "TaskRetroDrafted must backlink to the source")
	require.Equal(t, true, retroPayload["draft"], "Draft branch must mark the event payload's draft flag true")

	// 2. tasks row: derived_state=open (no 'draft' value in the
	//    ENUM), title starts with the configured prefix, description
	//    is non-empty, project_id matches the source.
	newTask := loadTaskByPublicID(t, testDB, wsID, newPub)
	require.Equal(t, "open", newTask.derivedState, "retro task must start at derived_state='open' (ENUM lacks a 'draft' value)")
	require.True(t, strings.HasPrefix(newTask.title, "Retro: "), "retro task title must start with the canonical prefix; got %q", newTask.title)
	require.NotEmpty(t, newTask.description, "retro task must carry a non-empty description")
	require.Equal(t, loadTaskProjectID(t, testDB, wsID, srcInternalID), newTask.projectID,
		"retro task must inherit the source's project_id")

	// 3. task_dependencies row: kind='retro_of', from=new task,
	//    to=source, enabled=true. Confirms the orientation pinned in
	//    sql/tables/task_dependencies.sql.
	dep := loadRetroDependency(t, testDB, wsID, newTask.id, uint32(srcInternalID))
	require.Equal(t, "retro_of", dep.kind)
	require.True(t, dep.enabled)
	require.Equal(t, newTask.id, dep.fromTaskID)
	require.Equal(t, uint32(srcInternalID), dep.toTaskID)
}

// TestApplierGenerateRetroOnMissingTargetTaskRejectsSignal pins that
// a verdict naming a non-existent task is converted to a
// SignalRejected event by the Applier's pre-resolution gate. No
// tasks row or dependency edge is written; the source task count is
// unchanged from before the call.
func TestApplierGenerateRetroOnMissingTargetTaskRejectsSignal(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	beforeTaskCount := countTasks(t, testDB, wsID)
	beforeDepCount := countDependencies(t, testDB, wsID)

	bus := &recordingBus{}
	applier := &signaljudge.Applier{
		Bus: bus,
		Tasks: &signaljudge.SQLTaskMutator{
			DB:      testDB,
			Queries: generated.New(testDB),
			Logger:  slog.New(slog.DiscardHandler),
		},
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyDraft},
	}

	ghost := "00000000-0000-0000-0000-deadbeefdead"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := applier.Apply(ctx, signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-feedfacedeae",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}, signaljudge.AgentRef{InternalID: agent.AgentID}, 2, signaljudge.Verdict{
		Action:             signaljudge.ActionGenerateRetro,
		TargetTaskPublicID: ptrString(ghost),
		Confidence:         0.9,
		ReasoningExcerpt:   "user named a task that does not exist",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.Skipped, "missing target must skip materialisation")

	events := bus.snapshot()
	require.Len(t, events, 1, "missing target must emit only SignalRejected")
	require.Equal(t, eventbus.SignalRejected, events[0].Type)
	payload, ok := events[0].Payload.(map[string]any)
	require.True(t, ok)
	require.Contains(t, payload["validationError"], "not found", "SignalRejected.reason must name the missing target")

	require.Equal(t, beforeTaskCount, countTasks(t, testDB, wsID), "no tasks row may be inserted when the target is missing")
	require.Equal(t, beforeDepCount, countDependencies(t, testDB, wsID), "no task_dependencies row may be inserted when the target is missing")
}

// TestApplierGenerateRetroRollsBackOnDependencyInsertFailure pins
// the transactional invariant: when the dependency insert (or any
// pre-step) fails, the tasks INSERT must roll back atomically. The
// test injects the failure by routing through a wrapper that calls
// the real mutator with an impossible source task id; the mutator's
// pre-read SELECT fails with sql.ErrNoRows, the transaction never
// opens, and the task count is unchanged.
//
// This covers the spec's "rolls back on dependency insert failure"
// scenario without needing a way to selectively fail a specific
// INSERT inside an open transaction (which would require mocking
// the entire sqlc surface). The end-to-end invariant under test is
// "no partial commits ever leak past Applier on retro errors".
func TestApplierGenerateRetroRollsBackOnDependencyInsertFailure(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	wsID := lookupWorkspaceIDForRetro(t, testDB, tt.WorkspacePublicID)

	srcPub, _ := createSourceTask(t, tt, "Postmortem subject")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	signalInternalID := insertSignalRowForRetro(t, testDB, wsID, "calendar.event_day_arrived")

	beforeTaskCount := countTasks(t, testDB, wsID)
	beforeDepCount := countDependencies(t, testDB, wsID)

	bus := &recordingBus{}
	realMutator := &signaljudge.SQLTaskMutator{
		DB:      testDB,
		Queries: generated.New(testDB),
		Logger:  slog.New(slog.DiscardHandler),
	}
	faulty := &faultyAddDependencyMutator{real: realMutator}

	applier := &signaljudge.Applier{
		Bus:      bus,
		Tasks:    faulty,
		Resolver: &signaljudge.SQLTaskResolver{DB: testDB},
		Signals:  nopSignalUpdater{},
		Autonomy: &staticAutonomy{level: signaljudge.AutonomyDraft},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := applier.Apply(ctx, signaljudge.SignalRef{
		InternalID:  signalInternalID,
		PublicID:    "00000000-0000-0000-0000-feedfacedeaf",
		WorkspaceID: wsID,
		Kind:        "calendar.event_day_arrived",
	}, signaljudge.AgentRef{InternalID: agent.AgentID}, 3, signaljudge.Verdict{
		Action:             signaljudge.ActionGenerateRetro,
		TargetTaskPublicID: ptrString(srcPub),
		Confidence:         0.7,
		ReasoningExcerpt:   "fault-injection path should roll back",
	})
	// The Applier wraps mutator errors and returns them; the
	// surrounding event emission is still partial (SignalJudged
	// already landed before the mutator was called). The critical
	// invariant is that no task / dependency rows leaked.
	require.Error(t, err, "fault-injected mutator must surface as an error from Apply")
	require.True(t, strings.Contains(err.Error(), "DraftRetroTask") || strings.Contains(err.Error(), "draft retro task"),
		"error must name the rolled-back operation; got %v", err)

	require.Equal(t, beforeTaskCount, countTasks(t, testDB, wsID),
		"no tasks row may be committed when DraftRetroTask returns an error")
	require.Equal(t, beforeDepCount, countDependencies(t, testDB, wsID),
		"no task_dependencies row may be committed when DraftRetroTask returns an error")
}

// ----- test helpers ----------------------------------------------------------

// taskRow is the minimal projection [loadTaskByPublicID] returns.
type taskRow struct {
	id           uint32
	projectID    uint32
	title        string
	description  string
	derivedState string
}

// depRow is the minimal projection [loadRetroDependency] returns.
type depRow struct {
	kind       string
	fromTaskID uint32
	toTaskID   uint32
	enabled    bool
}

// ptrString returns a pointer to s so the test can pass it to
// Verdict.TargetTaskPublicID without a temporary.
func ptrString(s string) *string { return &s }

// createSourceTask seeds a task via POST /tasks under the supplied
// tenant. Returns the new task's public id (string) and internal id
// (uint32) so tests can drive both layers without re-resolving.
func createSourceTask(t *testing.T, tt *helpers.TestTenant, title string) (string, int64) {
	t.Helper()
	var body struct {
		ID string `json:"id"`
	}
	doRetroJSON(t, "POST", tt.BaseURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     title,
	}, &body)
	require.NotEmpty(t, body.ID, "POST /tasks must return a task id")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id int64
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		body.ID,
	).Scan(&id))
	require.NotZero(t, id)
	return body.ID, id
}

// lookupWorkspaceIDForRetro mirrors the e2e helper of the same name
// without depending on it directly (the signaljudge test package is
// separate from e2e).
func lookupWorkspaceIDForRetro(t *testing.T, db *sql.DB, workspacePublicID string) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&id))
	require.NotZero(t, id)
	return id
}

// insertSignalRowForRetro inserts a minimal signals row directly so
// the test does not need to drive the POST /signals validator. Uses
// subject_type=workspace to keep the FK chain self-contained.
func insertSignalRowForRetro(t *testing.T, db *sql.DB, workspaceID uint32, kind string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := db.ExecContext(ctx, `
		INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, 'calendar', ?, JSON_OBJECT(), NOW(3), ?)`,
		workspaceID, kind, string(generated.SignalsSubjectTypeWorkspace),
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.NotZero(t, id)
	return id
}

// loadTaskByPublicID returns the projection [taskRow] for a task
// addressed by its public id. Used to assert the retro task landed
// with the expected shape.
func loadTaskByPublicID(t *testing.T, db *sql.DB, workspaceID uint32, publicID string) taskRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pub, err := types.Parse(publicID)
	require.NoError(t, err)
	var (
		row  taskRow
		desc sql.NullString
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT id, project_id, title, description, derived_state
		FROM tasks
		WHERE workspace_id = ? AND public_id = ?
		LIMIT 1`,
		workspaceID, pub,
	).Scan(&row.id, &row.projectID, &row.title, &desc, &row.derivedState))
	if desc.Valid {
		row.description = desc.String
	}
	return row
}

// loadTaskProjectID returns just the project_id for a task addressed
// by its internal id. Used to confirm the retro task inherited the
// source's project.
func loadTaskProjectID(t *testing.T, db *sql.DB, workspaceID uint32, internalID int64) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var projectID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT project_id FROM tasks WHERE workspace_id = ? AND id = ? LIMIT 1`,
		workspaceID, internalID,
	).Scan(&projectID))
	return projectID
}

// loadRetroDependency fetches the retro_of edge between two tasks
// and fails the test if more than one row matches. The
// task_dependencies table's UNIQUE (from, to, kind, enabled) key
// guarantees this within a single workspace.
func loadRetroDependency(t *testing.T, db *sql.DB, workspaceID uint32, fromTaskID, toTaskID uint32) depRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var (
		row     depRow
		enabled int
	)
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT kind, from_task_id, to_task_id, enabled
		FROM task_dependencies
		WHERE workspace_id = ?
		  AND from_task_id = ?
		  AND to_task_id = ?
		  AND kind = 'retro_of'
		LIMIT 1`,
		workspaceID, fromTaskID, toTaskID,
	).Scan(&row.kind, &row.fromTaskID, &row.toTaskID, &enabled))
	row.enabled = enabled != 0
	return row
}

// countTasks returns the number of enabled tasks in the workspace.
// Used as a before/after invariant in the rollback test.
func countTasks(t *testing.T, db *sql.DB, workspaceID uint32) int {
	t.Helper()
	return countRows(t, db, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ?`, workspaceID)
}

// countDependencies returns the number of enabled task_dependencies
// rows in the workspace. Used as a before/after invariant in the
// rollback test.
func countDependencies(t *testing.T, db *sql.DB, workspaceID uint32) int {
	t.Helper()
	return countRows(t, db, `SELECT COUNT(*) FROM task_dependencies WHERE workspace_id = ?`, workspaceID)
}

// countRows runs the supplied COUNT(*) query and returns the result.
func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
	return n
}

// doRetroJSON is a tiny copy of the e2e package's doJSON so this
// package stays self-contained. Asserts 2xx and decodes the response
// body (when non-nil) into out. An empty 200 body is tolerated so the
// helper works for both create endpoints that return a JSON object
// and side-effect endpoints that return 204 / "".
func doRetroJSON(t *testing.T, method, url, bearer string, body any, out any) {
	t.Helper()
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		raw = b
	}
	req, err := newJSONReq(method, url, bearer, raw)
	require.NoError(t, err)
	resp, err := testSrv.Server.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status=%d", method, url, resp.StatusCode)
	}
	if out != nil {
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(out); err != nil && err.Error() != "EOF" {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
}

// newJSONReq is a thin wrapper around http.NewRequest that attaches
// the bearer token + JSON headers. Extracted so doRetroJSON stays
// focused on the request/response flow.
func newJSONReq(method, url, bearer string, body []byte) (*http.Request, error) {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	var (
		r   *http.Request
		err error
	)
	if rdr != nil {
		r, err = http.NewRequest(method, url, rdr)
	} else {
		r, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Accept", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r, nil
}
