package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notification"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// stubExecutor is an AgentExecutor implementation that returns a fixed
// ExecutionResult plus optional error so the integration test can drive
// each handoff branch without standing up a real LLM provider. Each
// branch (success / low_confidence / cost_cap / tool_error) is one
// instantiation.
type stubExecutor struct {
	result agentruntime.ExecutionResult
	err    error
}

func (s *stubExecutor) ExecuteAgent(_ context.Context, _, _ uint32) (agentruntime.ExecutionResult, error) {
	return s.result, s.err
}

// createTaskForAgent posts a fresh task to the tenant's default project
// via REST and returns its public id. We avoid relying on a separate
// helpers.CreateTask because the existing helpers wrap auth flows only.
func createTaskForAgent(t *testing.T, tt *helpers.TestTenant, title string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     title,
	}, &out)
	require.NotEmpty(t, out.ID, "task create returned empty id")
	return out.ID
}

// eventRow is the in-test scan target for the events table. Tests pull
// rows by (workspace, task, type) and assert on shape rather than
// re-using the v_task_timeline view because the integration tests want
// to verify the underlying row, not the timeline projection.
type eventRow struct {
	ID           uint64
	Type         string
	ActorUserID  sql.NullInt32
	ActorAgentID sql.NullInt32
	TaskID       sql.NullInt32
	Payload      json.RawMessage
}

func selectEventsForTask(t *testing.T, db *sql.DB, workspaceID, taskID uint32, eventType string) []eventRow {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, type, actor_user_id, actor_agent_id, task_id, payload_json
		FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = ?
		ORDER BY id ASC`,
		workspaceID, taskID, eventType,
	)
	require.NoError(t, err)
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var r eventRow
		require.NoError(t, rows.Scan(&r.ID, &r.Type, &r.ActorUserID, &r.ActorAgentID, &r.TaskID, &r.Payload))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// readAgentMemo fetches the JSON blob from tasks.agent_memo and decodes
// it into a map. Returns nil when the row is NULL — every successful
// memo merge produces a non-empty map.
func readAgentMemo(t *testing.T, db *sql.DB, workspaceID, taskID uint32) map[string]any {
	t.Helper()
	var raw sql.NullString
	err := db.QueryRow(
		`SELECT CAST(agent_memo AS CHAR) FROM tasks WHERE workspace_id = ? AND id = ?`,
		workspaceID, taskID,
	).Scan(&raw)
	require.NoError(t, err)
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil
	}
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(raw.String), &out))
	return out
}

// countTaskActors returns the count of task_actors rows that match the
// (task, agent, kind='agent', role='assignee') tuple with the desired
// enabled bit. Tests assert before / after handoff transitions.
func countAgentActors(t *testing.T, db *sql.DB, workspaceID, taskID, agentID uint32, enabled bool) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM task_actors
		WHERE workspace_id = ? AND task_id = ? AND agent_id = ?
		  AND kind = 'agent' AND role = 'assignee' AND enabled = ?`,
		workspaceID, taskID, agentID, enabled,
	).Scan(&n))
	return n
}

// TestHandoffToAgentEndpoint asserts POST /tasks/{id}/handoff/to-agent
// disables any prior agent assignee, attaches the new one, and writes
// an agent.task.handoff_to_agent event with actor_user_id set (human
// caller) and actor_agent_id NULL.
//
// Notification-row assertion is intentionally left to
// TestManualHandbackEndpoint: notification fan-out filters the actor
// out of the recipient set, and a fresh single-tenant test has exactly
// one workspace member (the caller / actor), so handoff_to_agent
// produces zero rows by design. The manual handback flow is the
// load-bearing path for the inbox-badge UX anyway (actor=agent,
// recipient=user) and is covered there.
func TestHandoffToAgentEndpoint(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff to agent task")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})

	// Sanity: no enabled agent actor yet.
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	require.Equal(t, 0, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true))

	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-agent",
		tt.AccessToken,
		map[string]any{"agentId": agent.AgentPublicID},
		nil,
	)

	// Exactly one enabled task_actors row for this agent on this task.
	require.Equal(t, 1, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true))

	// Exactly one agent.task.handoff_to_agent event with the caller as
	// actor_user_id and actor_agent_id NULL.
	evs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_agent")
	require.Len(t, evs, 1, "expected exactly one handoff_to_agent event")
	require.True(t, evs[0].ActorUserID.Valid, "handoff_to_agent must record the human caller")
	require.False(t, evs[0].ActorAgentID.Valid, "handoff_to_agent must leave actor_agent_id NULL")
}

// TestHandoffSuccessPath runs the orchestrator runner against a real DB
// for a healthy execution. Asserts ai.agent.run.started + completed get
// task_id + actor_agent_id stamped, agent_memo.attempts increments, and
// no handoff event is emitted.
func TestHandoffSuccessPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff success path")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	runner := &agentruntime.OrchestratorRunner{
		DB:       testDB,
		Queries:  generated.New(testDB),
		Executor: &stubExecutor{result: agentruntime.ExecutionResult{Confidence: 0.95, LastThought: "healthy"}},
	}

	// Seed a synthetic source event so resolveSourceTask resolves to
	// this task, mirroring how the on_event source would fan out.
	sourceEventID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	require.NoError(t, runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: sourceEventID},
		time.Now().UTC(),
	))

	started := selectEventsForTask(t, testDB, wsID, taskInternalID, "ai.agent.run.started")
	completed := selectEventsForTask(t, testDB, wsID, taskInternalID, "ai.agent.run.completed")
	failed := selectEventsForTask(t, testDB, wsID, taskInternalID, "ai.agent.run.failed")
	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, started, 1, "expected exactly one ai.agent.run.started")
	require.Len(t, completed, 1, "expected exactly one ai.agent.run.completed")
	require.Empty(t, failed, "successful run must not emit failed event")
	require.Empty(t, handoffs, "healthy run must not emit a handoff")

	// actor_agent_id is set, actor_user_id is NULL.
	for _, e := range append(started, completed...) {
		require.True(t, e.ActorAgentID.Valid, "%s missing actor_agent_id", e.Type)
		require.EqualValues(t, agent.AgentID, e.ActorAgentID.Int32)
		require.False(t, e.ActorUserID.Valid, "%s actor_user_id must be NULL", e.Type)
	}

	memo := readAgentMemo(t, testDB, wsID, taskInternalID)
	require.NotNil(t, memo, "agent_memo must be populated on success")
	require.EqualValues(t, 1, asNumber(t, memo["attempts"]))
	require.Equal(t, "healthy", memo["last_thought"])
	require.NotZero(t, asNumber(t, memo["last_finished_at"]))
}

// TestHandoffLowConfidence drives the low-confidence trigger. Asserts an
// agent.task.handoff_to_user event with reason=low_confidence and
// agent_memo.handoff_status=stuck.
func TestHandoffLowConfidence(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff low confidence")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	runner := &agentruntime.OrchestratorRunner{
		DB:       testDB,
		Queries:  generated.New(testDB),
		Executor: &stubExecutor{result: agentruntime.ExecutionResult{Confidence: 0.2}},
	}
	srcID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	require.NoError(t, runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: srcID},
		time.Now().UTC(),
	))

	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, handoffs, 1, "low_confidence must emit exactly one handoff_to_user event")
	require.True(t, handoffs[0].ActorAgentID.Valid)
	require.EqualValues(t, agent.AgentID, handoffs[0].ActorAgentID.Int32)
	require.False(t, handoffs[0].ActorUserID.Valid)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(handoffs[0].Payload, &payload))
	require.Equal(t, "low_confidence", payload["reason"])
	require.Equal(t, agent.AgentPublicID, payload["agentPublicId"])

	memo := readAgentMemo(t, testDB, wsID, taskInternalID)
	require.Equal(t, "stuck", memo["handoff_status"])
	require.Equal(t, "low_confidence", memo["handoff_reason"])
	require.EqualValues(t, 1, asNumber(t, memo["handoff_count"]))
}

// TestHandoffCostCap drives the cost_cap branch through the failure
// path. Asserts the handoff event has reason=cost_cap and ai_agents.paused
// flips to TRUE.
func TestHandoffCostCap(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff cost cap")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{MonthlyCostCapCents: 1})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	runner := &agentruntime.OrchestratorRunner{
		DB:      testDB,
		Queries: generated.New(testDB),
		Executor: &stubExecutor{
			result: agentruntime.ExecutionResult{CostCapHit: true},
			err:    errors.New("cost cap exceeded"),
		},
	}
	srcID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	// Run() returns the executor error on the failure branch — that is
	// expected, the surrounding queue would Nack and retry. We swallow
	// it here because the assertion targets the side effects.
	_ = runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: srcID},
		time.Now().UTC(),
	)

	failed := selectEventsForTask(t, testDB, wsID, taskInternalID, "ai.agent.run.failed")
	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, failed, 1, "cost_cap must emit ai.agent.run.failed")
	require.Len(t, handoffs, 1, "cost_cap must also emit a handoff_to_user")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(handoffs[0].Payload, &payload))
	require.Equal(t, "cost_cap", payload["reason"])

	var paused bool
	require.NoError(t, testDB.QueryRow(
		`SELECT paused FROM ai_agents WHERE id = ?`, agent.AgentID,
	).Scan(&paused))
	require.True(t, paused, "cost_cap must flip ai_agents.paused")
}

// TestHandoffToolError drives the tool_error branch (>= 3 consecutive
// tool failures on a successful run).
func TestHandoffToolError(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff tool error")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	runner := &agentruntime.OrchestratorRunner{
		DB:       testDB,
		Queries:  generated.New(testDB),
		Executor: &stubExecutor{result: agentruntime.ExecutionResult{ConsecutiveToolFailures: 3}},
	}
	srcID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	require.NoError(t, runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: srcID},
		time.Now().UTC(),
	))

	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, handoffs, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(handoffs[0].Payload, &payload))
	require.Equal(t, "tool_error", payload["reason"])
}

// TestHandoffLoopDetected pre-seeds agent_memo.handoff_count at the
// limit so the next handoff trigger emits ai.agent.run.failed with
// HANDOFF_LOOP_DETECTED instead of another handoff event.
func TestHandoffLoopDetected(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff loop detected")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	// Pre-seed handoff_count at the loop limit so the runner's next
	// classify pass routes through handleHandoff's loop-budget branch.
	helpers.SetTaskAgentMemo(t, testDB, tt.WorkspacePublicID, taskID, map[string]any{
		"handoff_count":  2,
		"handoff_status": "stuck",
	})

	runner := &agentruntime.OrchestratorRunner{
		DB:               testDB,
		Queries:          generated.New(testDB),
		Executor:         &stubExecutor{result: agentruntime.ExecutionResult{Confidence: 0.2}},
		HandoffLoopLimit: 2,
	}
	srcID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	require.NoError(t, runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: srcID},
		time.Now().UTC(),
	))

	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Empty(t, handoffs, "loop-exhausted run must NOT emit another handoff event")

	failed := selectEventsForTask(t, testDB, wsID, taskInternalID, "ai.agent.run.failed")
	require.NotEmpty(t, failed, "loop detection must emit ai.agent.run.failed")
	// Find the failure that carries the structured error code.
	var foundLoop bool
	for _, e := range failed {
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		if errStr, ok := p["error"].(string); ok && strings.Contains(errStr, "HANDOFF_LOOP_DETECTED") {
			foundLoop = true
			break
		}
	}
	require.True(t, foundLoop, "expected a HANDOFF_LOOP_DETECTED failure event")

	memo := readAgentMemo(t, testDB, wsID, taskInternalID)
	require.Equal(t, "loop_detected", memo["handoff_status"])
}

// TestManualHandbackEndpoint exercises POST /tasks/{id}/handoff/to-user
// (human-initiated handback). Asserts the agent assignee disables, the
// event payload tags reason=manual, and agent_memo writes handed_back.
// Also drives the notification fan-out directly so the assertion that a
// notifications row materialises for the recipient stays close to the
// underlying event row — the test server does not wire the production
// hook, matching the pattern in notification_dispatch_test.go.
func TestManualHandbackEndpoint(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Manual handback")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	userInternalID := lookupUserInternalID(context.Background(), t, testDB, tt.UserPublicID)

	beforeCount := notificationCountForUser(context.Background(), t, testDB, userInternalID)

	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-user",
		tt.AccessToken,
		map[string]any{"reason": "manual"},
		nil,
	)

	require.Equal(t, 0, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true),
		"manual handback must disable the agent assignee")
	require.Equal(t, 1, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, false),
		"the prior agent row remains for audit")

	evs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, evs, 1)
	require.True(t, evs[0].ActorAgentID.Valid, "handoff_to_user must stamp the prior agent as actor")
	require.EqualValues(t, agent.AgentID, evs[0].ActorAgentID.Int32)
	require.False(t, evs[0].ActorUserID.Valid)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(evs[0].Payload, &payload))
	require.Equal(t, "manual", payload["reason"])
	require.Equal(t, agent.AgentPublicID, payload["priorAgentPublicId"])

	memo := readAgentMemo(t, testDB, wsID, taskInternalID)
	require.Equal(t, "handed_back", memo["handoff_status"])
	require.Equal(t, "manual", memo["handoff_reason"])

	// Drive the fan-out manually with the event id we just observed so
	// the notification row assertion verifies the classifyEvent wiring
	// for agent.task.handoff_to_user without relying on the test server
	// to register the production hook.
	queries := generated.New(testDB)
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	hook := f.Hook()
	hook(context.Background(), wsID, "agent.task.handoff_to_user", uint32(evs[0].ID)) //#nosec G115 -- events.id fits uint32 within realistic test sizes
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	afterCount := notificationCountForUser(context.Background(), t, testDB, userInternalID)
	require.Equalf(t, int64(1), afterCount-beforeCount,
		"agent.task.handoff_to_user must dispatch exactly one notification to the workspace member (before=%d after=%d)",
		beforeCount, afterCount)

	// Verify the dispatched row points at the right event, recipient,
	// and resource type. severity=high (per classifyEvent) is the
	// load-bearing assertion for the inbox-badge UX.
	var (
		gotRecipientID  uint32
		gotSourceEvent  sql.NullInt64
		gotEventType    string
		gotResourceType string
		gotSeverity     string
	)
	require.NoError(t, testDB.QueryRowContext(context.Background(), `
		SELECT recipient_user_id, source_event_id, event_type, resource_type, severity
		FROM notifications
		WHERE recipient_user_id = ?
		  AND event_type = 'agent.task.handoff_to_user'
		ORDER BY id DESC
		LIMIT 1
	`, userInternalID).Scan(&gotRecipientID, &gotSourceEvent, &gotEventType, &gotResourceType, &gotSeverity))
	require.Equal(t, userInternalID, gotRecipientID)
	require.True(t, gotSourceEvent.Valid, "source_event_id must be populated by fan-out")
	require.EqualValues(t, evs[0].ID, gotSourceEvent.Int64)
	require.Equal(t, "agent.task.handoff_to_user", gotEventType)
	require.Equal(t, "task", gotResourceType)
	require.Equal(t, "high", gotSeverity)
}

// TestAgentRunsList exercises GET /tasks/{id}/agent-runs. Seeds two
// synthetic ai.agent.run.* events for the task and asserts the response
// returns them in occurred_at DESC order and that the limit query
// parameter caps at 100.
func TestAgentRunsList(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Agent runs list")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	runner := &agentruntime.OrchestratorRunner{
		DB:       testDB,
		Queries:  generated.New(testDB),
		Executor: &stubExecutor{result: agentruntime.ExecutionResult{Confidence: 0.9, LastThought: "ok"}},
	}
	srcID := insertSyntheticTaskEvent(t, testDB, wsID, taskInternalID)
	require.NoError(t, runner.Run(context.Background(),
		agentruntime.Job{AgentID: agent.AgentID, WsID: wsID, SourceEventID: srcID},
		time.Now().UTC(),
	))

	var resp struct {
		Total int64 `json:"total"`
		Runs  []struct {
			EventID    string `json:"eventId"`
			Type       string `json:"type"`
			OccurredAt int64  `json:"occurredAt"`
			Agent      struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"agent"`
		} `json:"runs"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID+"/agent-runs?limit=100",
		tt.AccessToken, nil, &resp,
	)
	require.GreaterOrEqual(t, len(resp.Runs), 2, "expected started+completed at minimum")
	require.LessOrEqual(t, len(resp.Runs), 100, "agent-runs response must not exceed the documented ceiling")

	// The OpenAPI schema declares limit<=100; over-ceiling values must
	// be rejected at the validation layer rather than silently capped.
	// Asserting on 422 keeps the contract honest if a future patch
	// loosens the cap.
	overStatus, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID+"/agent-runs?limit=200",
		tt.AccessToken, nil,
	)
	require.Equal(t, http.StatusUnprocessableEntity, overStatus,
		"limit > 100 must be rejected by openapi validation")

	// Every row carries the expected agent identifiers.
	for _, r := range resp.Runs {
		require.Equal(t, agent.AgentPublicID, r.Agent.ID)
		require.Equal(t, agent.Name, r.Agent.Name)
		require.True(t, strings.HasPrefix(r.Type, "ai.agent.run."), "type prefix: %s", r.Type)
	}

	// Ordering: occurredAt DESC.
	for i := 1; i < len(resp.Runs); i++ {
		require.LessOrEqual(t, resp.Runs[i].OccurredAt, resp.Runs[i-1].OccurredAt,
			"runs must be ordered by occurredAt DESC")
	}
}

// TestAutoActionsHandoffToUser drives the autoactions executor against a
// task whose agent assignee has been "attempting" the task long enough
// for the stuck rule to fire. Asserts the handoff event lands with
// reason=stuck / detectedBy=auto_action and the agent assignee is
// disabled.
func TestAutoActionsHandoffToUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Autoactions stuck")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	// Stuck signal: attempts >= 3 and last_finished_at older than the
	// rule's idle window (>4h). Also age the task's updated_at past the
	// idle threshold so the executor's WHERE picks it up.
	helpers.SetTaskAgentMemo(t, testDB, tt.WorkspacePublicID, taskID, map[string]any{
		"attempts":         5,
		"last_finished_at": time.Now().Add(-12 * time.Hour).Unix(),
	})
	ageTaskUpdatedAt(t, testDB, wsID, taskInternalID, 12*time.Hour)

	exec := &autoactions.Executor{
		DB:     testDB,
		Logger: testLogger(t),
		Config: autoactions.ExecutorConfig{
			ConfidenceThreshold: 0.5,
		},
	}
	exec.RunOnce(context.Background())

	handoffs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, handoffs, 1, "autoactions executor must emit exactly one handoff")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(handoffs[0].Payload, &payload))
	require.Equal(t, "stuck", payload["reason"])
	require.Equal(t, "auto_action", payload["detectedBy"])

	require.Equal(t, 0, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true),
		"autoactions must disable the agent assignee")

	memo := readAgentMemo(t, testDB, wsID, taskInternalID)
	require.Equal(t, "handed_back", memo["handoff_status"])
	require.Equal(t, "stuck", memo["handoff_reason"])
}

// asNumber coerces a JSON-decoded number (which lands as float64 via
// encoding/json on a generic map[string]any) into a float64 the
// assertion can compare. Tests should still use EqualValues for the
// loose equality with integer literals.
func asNumber(t *testing.T, v any) float64 {
	t.Helper()
	n, ok := v.(float64)
	require.Truef(t, ok, "expected number, got %T (%v)", v, v)
	return n
}

// insertSyntheticTaskEvent appends a task.created-shaped event row so
// the orchestrator's resolveSourceTask call returns a non-zero task_id.
// The orchestrator only reads (task_id, public_id) from the row, so the
// payload is intentionally minimal.
func insertSyntheticTaskEvent(t *testing.T, db *sql.DB, workspaceID, taskID uint32) uint32 {
	t.Helper()
	res, err := helpers.ExecRetry(context.Background(), db, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, type, payload_json, occurred_at)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, 'test.synthetic.source', JSON_OBJECT(), NOW(3))`,
		workspaceID, taskID,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- test fixture
}

// ageTaskUpdatedAt pushes the row's updated_at back so the autoactions
// idle filter (default 4h for KindHandoffToUser) picks the task up.
func ageTaskUpdatedAt(t *testing.T, db *sql.DB, workspaceID, taskID uint32, delta time.Duration) {
	t.Helper()
	_, err := db.Exec(`
		UPDATE tasks SET updated_at = DATE_SUB(NOW(), INTERVAL ? SECOND)
		WHERE workspace_id = ? AND id = ?`,
		int64(delta.Seconds()), workspaceID, taskID,
	)
	require.NoError(t, err)
}

// testLogger returns a logger that writes to t.Log so autoactions error
// branches surface in the test output without spamming stdout.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(&tbWriter{tb: t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// tbWriter adapts testing.TB.Log into an io.Writer for slog.NewTextHandler.
type tbWriter struct{ tb testing.TB }

func (w *tbWriter) Write(p []byte) (int, error) {
	w.tb.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
