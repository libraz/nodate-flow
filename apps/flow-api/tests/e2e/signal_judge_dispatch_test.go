package e2e

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestSignalJudgeEnqueueOnManualSignal asserts that, after a signal
// row lands via POST /signals, the signaljudge.Enqueuer picks up the
// matching signal_judge agent and produces an in-process queue Run
// whose dedupe_key, Job.AgentID, and Job.WsID match the agent and
// signal.
//
// The test server harness does not wire signaljudge.Enqueuer into the
// router today (production wiring lives in cmd/api/main.go), so the
// test drives the enqueuer directly against the same DB. This still
// covers the Phase 2 J1 surface: the SELECT match query, the dedupe
// key shape, the Job.DedupeKey propagation, and the dispatch-arm side
// of the orchestrator runner is covered by the unit tests in
// internal/ai/agentruntime/judge_dispatch_test.go.
//
// TODO(phase-5): once the test server wires JudgeEnqueuer through
// router.Build, replace the direct enq.EnqueueForSignal call with an
// assertion that POST /signals alone produces the queue row.
func TestSignalJudgeEnqueueOnManualSignal(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Seed a signal_judge agent with an empty (wildcard)
	// event_trigger_types so it matches every kind. The helper's
	// default schedule_kind=manual keeps the interval scheduler out
	// of the picture.
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{
		Kind: "signal_judge",
	})

	// POST a manual signal via the public API so the full handler
	// path (workspace resolve, subject resolution, InsertSignal) runs
	// as it would in production.
	var signal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
		"payload":     map[string]any{"hello": "world"},
	}, &signal)
	require.NotEmpty(t, signal.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsID := lookupWorkspaceID(t, testDB, tt.WorkspacePublicID)
	signalInternalID := lookupSignalID(t, testDB, wsID, signal.ID)

	queue := agentruntime.NewInProcessQueue(8)
	enq := &signaljudge.Enqueuer{DB: testDB, Queue: queue}
	require.NoError(t, enq.EnqueueForSignal(ctx, signalInternalID, wsID, signalkinds.Manual))

	// Drain one run and assert the shape.
	claimCtx, claimCancel := context.WithTimeout(ctx, 2*time.Second)
	defer claimCancel()
	run, err := queue.Claim(claimCtx)
	require.NoError(t, err, "queue.Claim should yield the just-enqueued run")
	require.Equal(t, signaljudge.DedupeKeyForSignal(agent.AgentID, signalInternalID), run.DedupeKey)
	require.Equal(t, agent.AgentID, run.Job.AgentID)
	require.Equal(t, wsID, run.Job.WsID)
	require.Equal(t, run.DedupeKey, run.Job.DedupeKey, "Job.DedupeKey must mirror Run.DedupeKey so the runner can dispatch")
}

// TestSignalJudgeEnqueueRespectsKindFilter asserts an agent whose
// event_trigger_types is a non-empty array only matches when the
// signal's kind is in that array.
func TestSignalJudgeEnqueueRespectsKindFilter(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{
		Kind:              "signal_judge",
		EventTriggerTypes: `["discord.presence"]`,
	})

	wsID := lookupWorkspaceID(t, testDB, tt.WorkspacePublicID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// `manual` does NOT match the agent's filter.
	manualSignalID := insertSignalRowForTest(t, testDB, wsID, "manual", "manual")
	queue := agentruntime.NewInProcessQueue(8)
	enq := &signaljudge.Enqueuer{DB: testDB, Queue: queue}
	require.NoError(t, enq.EnqueueForSignal(ctx, manualSignalID, wsID, signalkinds.Manual))

	noRunCtx, noRunCancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer noRunCancel()
	if _, err := queue.Claim(noRunCtx); err == nil {
		t.Fatalf("non-matching kind should not have produced a judge run")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}

	// `discord.presence` DOES match.
	discordSignalID := insertSignalRowForTest(t, testDB, wsID, "discord.presence", "manual")
	require.NoError(t, enq.EnqueueForSignal(ctx, discordSignalID, wsID, signalkinds.DiscordPresence))
	matchCtx, matchCancel := context.WithTimeout(ctx, 2*time.Second)
	defer matchCancel()
	run, err := queue.Claim(matchCtx)
	require.NoError(t, err, "matching-kind signal should enqueue a judge run")
	require.Equal(t, signaljudge.DedupeKeyForSignal(agent.AgentID, discordSignalID), run.DedupeKey)
}

// TestSignalJudgeRunnerDispatchesToJudgeRunner pins the dispatch
// behaviour at the orchestrator boundary against a real database. A
// stub JudgeExecutor counts invocations so the test can assert that
// claiming a judge-shaped dedupe_key routes through ExecuteJudge and
// never touches the task-agent path.
func TestSignalJudgeRunnerDispatchesToJudgeRunner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{
		Kind: "signal_judge",
	})
	wsID := lookupWorkspaceID(t, testDB, tt.WorkspacePublicID)
	signalInternalID := insertSignalRowForTest(t, testDB, wsID, "manual", "manual")

	taskCalls := 0
	judge := &countingJudge{}
	runner := &agentruntime.OrchestratorRunner{
		DB:      testDB,
		Queries: generated.New(testDB),
		Executor: stubExecutorFunc(func(_ context.Context, _, _ uint32) (agentruntime.ExecutionResult, error) {
			taskCalls++
			return agentruntime.ExecutionResult{}, nil
		}),
		Judge: judge,
	}

	job := agentruntime.Job{
		AgentID:   agent.AgentID,
		WsID:      wsID,
		DedupeKey: signaljudge.DedupeKeyForSignal(agent.AgentID, signalInternalID),
	}
	require.NoError(t, runner.Run(context.Background(), job, time.Now().UTC()))
	require.Equal(t, 1, judge.calls, "judge-shaped dedupe key must drive ExecuteJudge")
	require.Equal(t, 0, taskCalls, "judge dispatch must not also call the task-agent executor")
	require.Equal(t, signalInternalID, judge.gotSignal)
}

// countingJudge records how often ExecuteJudge fires and what signal
// id the runner passed. The orchestrator runner does not care about
// the returned ExecutionResult for this test.
type countingJudge struct {
	calls     int
	gotSignal int64
}

func (c *countingJudge) ExecuteJudge(_ context.Context, _, _ uint32, signalID int64) (agentruntime.ExecutionResult, error) {
	c.calls++
	c.gotSignal = signalID
	return agentruntime.ExecutionResult{LastThought: "stub"}, nil
}

// stubExecutorFunc adapts a plain func into the agentruntime.AgentExecutor
// interface so tests can spy on the task-agent path.
type stubExecutorFunc func(ctx context.Context, workspaceID, agentID uint32) (agentruntime.ExecutionResult, error)

func (f stubExecutorFunc) ExecuteAgent(ctx context.Context, workspaceID, agentID uint32) (agentruntime.ExecutionResult, error) {
	return f(ctx, workspaceID, agentID)
}

// lookupWorkspaceID resolves a workspace public id to its internal
// row id. Returns 0 on miss; the test fails immediately via require.
func lookupWorkspaceID(t *testing.T, db *sql.DB, workspacePublicID string) uint32 {
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

// lookupSignalID resolves a signals public id to its internal row id
// within the supplied workspace.
func lookupSignalID(t *testing.T, db *sql.DB, workspaceID uint32, signalPublicID string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM signals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, signalPublicID,
	).Scan(&id))
	require.NotZero(t, id)
	return id
}

// insertSignalRowForTest inserts a minimal signals row directly so
// tests can drive the enqueuer without going through the kind
// validation branch of POST /signals. The row uses
// subject_type=workspace so the NOT NULL constraint is satisfied
// without needing a real subject FK target.
func insertSignalRowForTest(t *testing.T, db *sql.DB, workspaceID uint32, kind, source string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := db.ExecContext(ctx, `
		INSERT INTO signals (public_id, workspace_id, source, kind, payload_json, received_at, subject_type)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, JSON_OBJECT(), NOW(3), ?)`,
		workspaceID, source, kind, string(generated.SignalsSubjectTypeWorkspace),
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
