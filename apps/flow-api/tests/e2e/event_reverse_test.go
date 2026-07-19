package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// Phase 3 / J5 — POST /workspaces/{wsId}/events/{eventPublicId}/reverse.
//
// These tests cover the six scenarios pinned by the release-8 plan:
//
//  1. LLM-origin event reverse succeeds (201 + reverses_event_id link).
//  2. Double reverse of the same event returns 409 ALREADY_REVERSED.
//  3. User-actor event reverse returns 403 NOT_LLM_ORIGIN.
//  4. System-source event reverse returns 403 NOT_LLM_ORIGIN.
//  5. derived_state cancellation: reversing a TaskAutoCompleted walks the
//     task out of `done` via the canonical reopen transition.
//  6. Unknown event public_id returns 404 TARGET_NOT_FOUND.

// reverseStatus issues POST .../reverse and returns (status, response body).
func reverseStatus(t *testing.T, tt *helpers.TestTenant, eventPublicID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t,
		http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/events/"+eventPublicID+"/reverse",
		tt.AccessToken,
		nil,
	)
}

// appendAgentEventForTask inserts an agent-actor event with the given
// type bound to (workspace, task, agent). Returns the row's
// (internal id, public id). Used to seed test fixtures that the
// reverse handler will then resolve via FindEventForReverse.
func appendAgentEventForTask(t *testing.T, db *sql.DB, wsID, taskID, agentID uint32, eventType string) (uint64, string) {
	t.Helper()
	pub := types.New()
	res, err := helpers.ExecRetry(context.Background(), db, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_agent_id, type, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, ?, JSON_OBJECT(), NOW(3))`,
		pub, wsID, taskID, agentID, eventType,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.GreaterOrEqual(t, id, int64(0))
	return uint64(id), pub.UUID().String() //#nosec G115 -- LastInsertId is asserted non-negative above.
}

// appendUserEventForTask inserts a user-actor event for the given
// (workspace, task, user). Used to seed the NOT_LLM_ORIGIN test path.
func appendUserEventForTask(t *testing.T, db *sql.DB, wsID, taskID, userID uint32, eventType string) string {
	t.Helper()
	pub := types.New()
	_, err := helpers.ExecRetry(context.Background(), db, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, type, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, ?, JSON_OBJECT(), NOW(3))`,
		pub, wsID, taskID, userID, eventType,
	)
	require.NoError(t, err)
	return pub.UUID().String()
}

// appendSystemEventForTask inserts a worker-tick event bound to
// (workspace, task). Used to seed the NOT_LLM_ORIGIN system-source
// test path; mirrors how the Phase 5 worker would emit reconciliation
// events with actor_system_source set instead of any actor FK.
func appendSystemEventForTask(t *testing.T, db *sql.DB, wsID, taskID uint32, source, eventType string) string {
	t.Helper()
	pub := types.New()
	_, err := helpers.ExecRetry(context.Background(), db, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_system_source, type, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, ?, JSON_OBJECT(), NOW(3))`,
		pub, wsID, taskID, source, eventType,
	)
	require.NoError(t, err)
	return pub.UUID().String()
}

// lookupUserIDByPublicID returns the internal users.id for a given
// public_id; used so the user-event seed can bind actor_user_id to a
// real row (otherwise the FK would reject the insert).
func lookupUserIDByPublicID(t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, db.QueryRow(
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		publicID,
	).Scan(&id))
	return id
}

// selectReverseRow scans the latest events row whose reverses_event_id
// matches the given target internal id. Returns the row's public_id,
// type, and actor_user_id so the test can assert the compensating
// event's shape.
type reverseRow struct {
	PublicID    types.PublicID
	Type        string
	ActorUser   sql.NullInt32
	ActorAgent  sql.NullInt32
	OccurredAt  time.Time
	PayloadJSON []byte
}

func selectReverseRow(t *testing.T, db *sql.DB, wsID uint32, reversesEventID uint64) reverseRow {
	t.Helper()
	var r reverseRow
	err := db.QueryRow(`
		SELECT public_id, type, actor_user_id, actor_agent_id, occurred_at, CAST(payload_json AS CHAR)
		FROM events
		WHERE workspace_id = ?
		  AND reverses_event_id = ?
		  AND enabled = TRUE
		ORDER BY id DESC LIMIT 1`,
		wsID, reversesEventID,
	).Scan(&r.PublicID, &r.Type, &r.ActorUser, &r.ActorAgent, &r.OccurredAt, &r.PayloadJSON)
	require.NoError(t, err, "expected exactly one reverse row for target %d", reversesEventID)
	return r
}

// readDerivedStateForReverse selects tasks.derived_state by internal
// id. The derived_state cancellation test asserts this transitions
// back to a non-done value after reverse. Locally named to avoid
// collision with the autoactions test's [readDerivedState] helper.
func readDerivedStateForReverse(t *testing.T, db *sql.DB, wsID, taskID uint32) string {
	t.Helper()
	var state string
	require.NoError(t, db.QueryRow(
		`SELECT derived_state FROM tasks WHERE workspace_id = ? AND id = ? LIMIT 1`,
		wsID, taskID,
	).Scan(&state))
	return state
}

// TestReverseLLMEventSucceeds covers case 1: an agent-actor event is
// reversed successfully, the 201 response carries the new event's
// public_id + occurred_at, and the DB now contains exactly one
// compensating row whose reverses_event_id points back.
func TestReverseLLMEventSucceeds(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: LLM origin")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	// Seed an agent-actor event. Use a non-judge type so the seed insert
	// does not need to take the AppendJudgeEvent path.
	origID, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
		"ai.agent.run.completed")

	status, body := reverseStatus(t, tt, origPub)
	require.Equal(t, http.StatusCreated, status, "reverse should return 201; body=%s", string(body))

	var out struct {
		PublicID   string `json:"publicId"`
		OccurredAt int64  `json:"occurredAt"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotEmpty(t, out.PublicID, "response must carry the new event's public_id")
	require.NotZero(t, out.OccurredAt, "response must carry occurredAt unix seconds")

	row := selectReverseRow(t, testDB, wsID, origID)
	require.Equal(t, out.PublicID, row.PublicID.UUID().String(),
		"DB row public_id must match the response")
	require.Equal(t, "ai.agent.run.completed", row.Type,
		"compensating event must reuse the original type")
	require.True(t, row.ActorUser.Valid, "reverser is the user, actor_user_id must be set")
	require.False(t, row.ActorAgent.Valid, "actor_agent_id must be NULL on the compensating row")

	// Payload carries the lineage links.
	var payload map[string]string
	require.NoError(t, json.Unmarshal(row.PayloadJSON, &payload))
	require.Equal(t, origPub, payload["reversed_event_public_id"])
	require.NotEmpty(t, payload["reversed_by_user_public_id"])
}

// TestReverseTwiceReturns409 covers case 2: a second reverse of the
// same target returns AI.REVERSE.ALREADY_REVERSED.
func TestReverseTwiceReturns409(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: double")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	_, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
		"ai.agent.run.completed")

	status, _ := reverseStatus(t, tt, origPub)
	require.Equal(t, http.StatusCreated, status, "first reverse must succeed")

	status2, body2 := reverseStatus(t, tt, origPub)
	require.Equal(t, http.StatusConflict, status2,
		"second reverse must return 409; body=%s", string(body2))
	require.Contains(t, string(body2), "AI.REVERSE.ALREADY_REVERSED",
		"error envelope must carry the canonical code; body=%s", string(body2))
}

// TestReverseUserEventReturns403 covers case 3: a user-actor event is
// not LLM-origin so the reverse path refuses with 403.
func TestReverseUserEventReturns403(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: user origin")
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	userID := lookupUserIDByPublicID(t, testDB, tt.UserPublicID)

	origPub := appendUserEventForTask(t, testDB, wsID, taskInternalID, userID, "task.updated")

	status, body := reverseStatus(t, tt, origPub)
	require.Equal(t, http.StatusForbidden, status,
		"user-origin reverse must return 403; body=%s", string(body))
	require.Contains(t, string(body), "AI.REVERSE.NOT_LLM_ORIGIN",
		"error envelope must carry the canonical code; body=%s", string(body))
}

// TestReverseSystemEventReturns403 covers case 4: a worker-tick event
// (actor_system_source set, actor_user_id + actor_agent_id NULL) is
// also not LLM-origin, so the reverse path refuses with 403.
func TestReverseSystemEventReturns403(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: system origin")
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	origPub := appendSystemEventForTask(t, testDB, wsID, taskInternalID,
		"worker.calendar", "calendar.event.synced")

	status, body := reverseStatus(t, tt, origPub)
	require.Equal(t, http.StatusForbidden, status,
		"system-source reverse must return 403; body=%s", string(body))
	require.Contains(t, string(body), "AI.REVERSE.NOT_LLM_ORIGIN",
		"error envelope must carry the canonical code; body=%s", string(body))
}

// TestReverseUnknownEventReturns404 covers case 6: an unknown
// public_id returns AI.REVERSE.TARGET_NOT_FOUND. The same 404 is
// returned for events that belong to a different workspace so cross-
// tenant probes cannot distinguish the two cases.
func TestReverseUnknownEventReturns404(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	// Random UUID v7 — has no matching events row.
	fake := types.New().UUID().String()

	status, body := reverseStatus(t, tt, fake)
	require.Equal(t, http.StatusNotFound, status,
		"unknown event must return 404; body=%s", string(body))
	require.Contains(t, string(body), "AI.REVERSE.TARGET_NOT_FOUND",
		"error envelope must carry the canonical code; body=%s", string(body))
}

// TestReverseCancelsDerivedState covers case 5: the load-bearing
// derived_state cancellation contract. We:
//
//  1. Seed a task and transition it to `done` via the engine path so
//     the row genuinely sits in the terminal state.
//  2. Append a TaskAutoCompleted event with actor_agent_id set
//     through the dedicated AppendJudgeEvent helper (this mirrors
//     what the Applier would emit for the same verdict).
//  3. Call reverse; assert the response is 201.
//  4. Assert the task's derived_state has been walked back out of
//     `done` (the canonical reopen transition lands on `waiting`).
//
// The append uses AppendJudgeEvent because TaskAutoCompleted is a
// judge-only kind; the seed therefore goes through the Applier-only
// gate the same way the production Applier would. The reverse handler
// itself goes through AppendReverseEvent which is the dedicated
// bypass for the J5 flow.
func TestReverseCancelsDerivedState(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Reverse: derived state cancellation")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	// Walk the task to `done` via the engine.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "complete"}, nil)
	require.Equal(t, "done", readDerivedStateForReverse(t, testDB, wsID, taskInternalID),
		"sanity: task must be done before reversal")

	// Seed a TaskAutoCompleted event so the reverse handler has a
	// LLM-origin target whose type lives in the rollback table.
	// AppendJudgeEvent bypasses the judge-kind guard the same way the
	// Applier does in production.
	taskFK := int64(taskInternalID)
	agentFK := int64(agent.AgentID)
	require.NoError(t, eventbus.AppendJudgeEvent(context.Background(), testDB, eventbus.Event{
		Type:         eventbus.TaskAutoCompleted,
		WorkspaceID:  wsID,
		ActorAgentID: &agentFK,
		TaskID:       &taskFK,
		Payload:      map[string]any{"reasoningExcerpt": "test", "confidence": 0.9},
	}))

	// Read the public_id of the row we just inserted so we can target
	// it via the reverse endpoint.
	q := generated.New(testDB)
	require.LessOrEqual(t, taskInternalID, uint32(math.MaxInt32))
	rows, err := q.ListAgentRunsByTask(context.Background(), generated.ListAgentRunsByTaskParams{
		WorkspaceID: wsID,
		TaskID:      sql.NullInt32{Int32: int32(taskInternalID), Valid: true}, //#nosec G115 -- bounded by assertion above.
		Limit:       50,
	})
	require.NoError(t, err)
	// ListAgentRunsByTask filters to ai.agent.run.% — TaskAutoCompleted
	// is not in that family. Fall back to a direct query.
	_ = rows

	var origPub types.PublicID
	require.NoError(t, testDB.QueryRow(`
		SELECT public_id FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = ?
		  AND actor_agent_id IS NOT NULL
		ORDER BY id DESC LIMIT 1`,
		wsID, taskInternalID, eventbus.TaskAutoCompleted,
	).Scan(&origPub))

	// Reverse the auto-completed event. Expect 201 and the task to
	// step back out of `done`.
	status, body := reverseStatus(t, tt, origPub.UUID().String())
	require.Equal(t, http.StatusCreated, status,
		"reverse must succeed for a TaskAutoCompleted target; body=%s", string(body))

	after := readDerivedStateForReverse(t, testDB, wsID, taskInternalID)
	require.NotEqual(t, "done", after,
		"derived_state must be walked out of done; got %q", after)
	// The canonical reopen transition from `done` lands on `waiting`
	// per the v1 state machine; pin that here so a future state-
	// machine change ripples into this assertion deliberately.
	require.True(t, strings.EqualFold(after, "waiting"),
		"expected reopen landing state `waiting`, got %q", after)
}
