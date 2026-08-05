package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// SeededAgent is the bundle of identifiers returned by SeedAgent and
// AssignAgent. It exposes both internal ids (needed by integration tests
// that simulate the orchestrator runner directly) and public_ids (needed
// for any caller that goes through the REST API).
type SeededAgent struct {
	// AgentID is the ai_agents.id internal FK. Tests that simulate the
	// orchestrator (which receives uint32 ids over its Job channel) need
	// the internal id; the REST surface only ever takes the public id.
	AgentID uint32
	// AgentPublicID is the UUID v7 (canonical string) used by every
	// REST endpoint that targets the agent.
	AgentPublicID string
	// ProviderID / ModelID / ModelPublicID are exposed so tests that
	// want to create additional agents inside the same workspace can
	// reuse the dependency chain rather than seeding it twice.
	ProviderID    uint32
	ModelID       uint32
	ModelPublicID string
	// Name is the human-readable label stored on ai_agents.name. Tests
	// assert against this when verifying the timeline / inbox surfaces
	// render the agent's display name.
	Name string
}

// SeedAgent provisions the minimum ai_providers + ai_models + ai_agents
// rows required to attach an agent to a task in tests.
//
// The handler-level POST /workspaces/{wsId}/ai/providers endpoint
// requires a real encryption Cipher and an encrypted api key, neither
// of which is wired through the test harness. Instead this helper
// inserts the rows directly under FOREIGN_KEY_CHECKS so the seed stays
// fully self-contained.
//
// Each call uses random suffixes for the provider / model / agent names
// so parallel-running tests inside the same workspace never collide.
// Returns a SeededAgent that carries both the internal and public ids.
func SeedAgent(t *testing.T, db *sql.DB, workspacePublicID string, opts SeedAgentOptions) *SeededAgent {
	t.Helper()
	require.NotNil(t, db, "SeedAgent requires a *sql.DB; call RegisterCleanupDB or pass testDB explicitly")
	require.NotEmpty(t, workspacePublicID, "SeedAgent requires a workspace public id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wsID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID)
	require.NoError(t, err, "lookup workspace id for %s", workspacePublicID)

	suffix := randomHex(6)

	providerPub := types.New()
	modelPub := types.New()
	agentPub := types.New()

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// FOREIGN_KEY_CHECKS off keeps the helper resilient if a later
	// schema change adds new FKs without exposing matching seed rows.
	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		require.NoError(t, err)
	}

	// ai_providers — api_key_ciphertext stays a meaningless byte blob
	// because the orchestrator path under test (handoff) never calls
	// the LLM. Production handlers reject empty ciphertext via the
	// Cipher abstraction; we bypass that here intentionally.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO ai_providers (
			public_id, workspace_id, kind, name,
			api_key_ciphertext, api_key_prefix, api_key_suffix,
			default_model, enabled
		) VALUES (?, ?, 'openai_compat', ?, ?, 'sk-test_', 'XXXX', NULL, TRUE)
	`, providerPub, wsID, fmt.Sprintf("test-provider-%s", suffix), []byte("test"))
	require.NoError(t, err, "insert ai_providers")
	providerInternalID := lastInsertID(t, res)

	// ai_models — minimal viable row with non-zero context window so the
	// agent runtime won't trip its sanity checks if a later code path
	// reads them.
	res, err = tx.ExecContext(ctx, `
		INSERT INTO ai_models (
			public_id, workspace_id, provider_id, name, display_name,
			context_window, max_output_tokens,
			input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
			supports_tools, supports_vision, enabled
		) VALUES (?, ?, ?, ?, ?, 128000, 4096, 0, 0, FALSE, FALSE, TRUE)
	`, modelPub, wsID, providerInternalID,
		fmt.Sprintf("test-model-%s", suffix),
		fmt.Sprintf("Test Model %s", suffix),
	)
	require.NoError(t, err, "insert ai_models")
	modelInternalID := lastInsertID(t, res)

	agentName := opts.Name
	if agentName == "" {
		agentName = fmt.Sprintf("Test Agent %s", suffix)
	}
	costCapNull := sql.NullInt32{}
	if opts.MonthlyCostCapCents > 0 {
		costCapNull = sql.NullInt32{Int32: int32(opts.MonthlyCostCapCents), Valid: true} //#nosec G115 -- test fixture
	}
	agentKind := opts.Kind
	if agentKind == "" {
		agentKind = "task_agent"
	}
	// event_trigger_types defaults to JSON_ARRAY() (wildcard); callers
	// that want to scope a signal_judge agent to specific kinds pass
	// a JSON literal in opts.EventTriggerTypes.
	eventTriggerExpr := "JSON_ARRAY()"
	var eventTriggerArgs []interface{}
	if opts.EventTriggerTypes != "" {
		eventTriggerExpr = "CAST(? AS JSON)"
		eventTriggerArgs = append(eventTriggerArgs, opts.EventTriggerTypes)
	}
	insertSQL := `
		INSERT INTO ai_agents (
			public_id, workspace_id, model_id, kind, name, description,
			system_prompt, temperature, monthly_cost_cap_cents,
			event_trigger_types,
			schedule_kind, paused, enabled
		) VALUES (?, ?, ?, ?, ?, ?, ?, 100, ?, ` + eventTriggerExpr + `, 'manual', FALSE, TRUE)
	`
	args := append([]interface{}{
		agentPub, wsID, modelInternalID, agentKind, agentName,
		"Test agent for handoff E2E",
		"You are an integration-test agent. Do not call any tools.",
		costCapNull,
	}, eventTriggerArgs...)
	res, err = tx.ExecContext(ctx, insertSQL, args...)
	require.NoError(t, err, "insert ai_agents")
	agentInternalID := lastInsertID(t, res)

	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	return &SeededAgent{
		AgentID:       uint32(agentInternalID), //#nosec G115 -- test fixture
		AgentPublicID: agentPub.UUID().String(),
		ProviderID:    uint32(providerInternalID), //#nosec G115 -- test fixture
		ModelID:       uint32(modelInternalID),    //#nosec G115 -- test fixture
		ModelPublicID: modelPub.UUID().String(),
		Name:          agentName,
	}
}

// SeedAgentOptions tunes the SeedAgent insert. Zero-value fields fall
// back to sensible defaults so callers only specify what they need.
type SeedAgentOptions struct {
	// Name overrides the auto-generated "Test Agent <hex>" label.
	Name string
	// MonthlyCostCapCents stamps ai_agents.monthly_cost_cap_cents. Use
	// a low value (e.g. 1) for the cost_cap handoff path.
	MonthlyCostCapCents int
	// Kind overrides the ai_agents.kind column. Empty defaults to
	// 'task_agent' to preserve every existing call site. Set to
	// 'signal_judge' to seed a judge agent (ADR 0008 D3).
	Kind string
	// EventTriggerTypes overrides ai_agents.event_trigger_types. Empty
	// leaves the column as the default JSON_ARRAY() (wildcard). Pass a
	// JSON array literal (e.g. `["manual","discord.presence"]`) to
	// scope a signal_judge agent to specific signal kinds.
	EventTriggerTypes string
}

// AssignAgentToTask attaches the seeded agent as the task's enabled
// assignee via task_actors. The helper uses direct SQL because the
// task-actors REST endpoints currently target human users; the
// HandoffToAgent endpoint is the only public path that promotes an
// agent, and using it from a fixture would couple the seed step to the
// handoff event we are about to assert against.
//
// Returns the task_actors.public_id so callers can assert on it.
func AssignAgentToTask(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string, agentInternalID uint32) string {
	t.Helper()
	require.NotNil(t, db, "AssignAgentToTask requires a *sql.DB")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wsID, taskID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		wsID, taskPublicID,
	).Scan(&taskID))

	actorPub := types.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO task_actors (
			public_id, workspace_id, task_id, agent_id, kind, role, enabled
		) VALUES (?, ?, ?, ?, 'agent', 'assignee', TRUE)
	`, actorPub, wsID, taskID, agentInternalID)
	require.NoError(t, err, "insert task_actors")
	return actorPub.UUID().String()
}

// SetTaskAgentMemo writes the supplied map into tasks.agent_memo via
// JSON_MERGE_PATCH semantics. Used by tests that need to pre-seed
// handoff_count or attempts to drive a specific branch (loop budget,
// autoactions stuck threshold).
func SetTaskAgentMemo(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string, memo map[string]any) {
	t.Helper()
	require.NotNil(t, db, "SetTaskAgentMemo requires a *sql.DB")
	require.NotEmpty(t, memo, "SetTaskAgentMemo requires a non-empty memo map")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := json.Marshal(memo)
	require.NoError(t, err)

	var wsID, taskID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&wsID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		wsID, taskPublicID,
	).Scan(&taskID))

	_, err = db.ExecContext(ctx,
		`UPDATE tasks SET agent_memo = JSON_MERGE_PATCH(COALESCE(agent_memo, '{}'), CAST(? AS JSON))
		 WHERE workspace_id = ? AND id = ?`,
		string(raw), wsID, taskID,
	)
	require.NoError(t, err, "update tasks.agent_memo")
}

// AgentTaskInternalIDs resolves the internal task id for a given task
// public id. Several handoff assertions need the internal id to query
// events / task_actors by FK; returning it from one place keeps the
// per-test scaffolding tight.
func AgentTaskInternalIDs(t *testing.T, db *sql.DB, workspacePublicID, taskPublicID string) (workspaceID uint32, taskID uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&workspaceID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, taskPublicID,
	).Scan(&taskID))
	return workspaceID, taskID
}

// lastInsertID extracts result.LastInsertId() and fails the test on error.
func lastInsertID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
