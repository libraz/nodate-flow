package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type fixedOnEventAgents struct {
	rows []agentruntime.OnEventAgentMatch
}

func (q fixedOnEventAgents) ListOnEventAgentsFor(context.Context, uint32, string) ([]agentruntime.OnEventAgentMatch, error) {
	return q.rows, nil
}

func (q fixedOnEventAgents) ListOnEventAgentsForEvent(context.Context, uint32, uint64) ([]agentruntime.OnEventAgentMatch, error) {
	return q.rows, nil
}

type triggerCaptureExecutor struct {
	trigger agentruntime.Trigger
}

func (e *triggerCaptureExecutor) ExecuteAgent(ctx context.Context, _, _ uint32) (agentruntime.ExecutionResult, error) {
	e.trigger = agentruntime.TriggerFromContext(ctx)
	return agentruntime.ExecutionResult{Confidence: 0.95, LastThought: "queued event reached the agent"}, nil
}

// TestMySQLAgentQueuePreservesSourceEvent covers the production queue
// boundary: an on_event replay is deduped by source row ID, distinct
// source rows remain distinct, and a fresh MySQLQueue instance claims
// the same source context. Running that claimed job must keep the
// trigger available to prompt construction and bind run history/memo to
// the source task. A zero-ID manual run must remain nullable in MySQL.
func TestMySQLAgentQueuePreservesSourceEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskPublicID := createTaskForAgent(t, tt, "MySQL queue source event")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskPublicID)
	sourceEventID := insertSyntheticTaskEvent(t, testDB, workspaceID, taskID)
	otherEventID := insertSyntheticTaskEvent(t, testDB, workspaceID, taskID)

	var clockCalls atomic.Int64
	base := time.Now().UTC()
	queue := agentruntime.NewMySQLQueue(testDB)
	trigger := &agentruntime.EventTrigger{
		Queries: fixedOnEventAgents{rows: []agentruntime.OnEventAgentMatch{{ID: agent.AgentID, WorkspaceID: workspaceID}}},
		Queue:   queue,
		Now: func() time.Time {
			return base.Add(time.Duration(clockCalls.Add(1)) * time.Second)
		},
	}
	notify := trigger.NotifyHook()
	notify(context.Background(), workspaceID, "test.synthetic.source", sourceEventID)
	notify(context.Background(), workspaceID, "test.synthetic.source", sourceEventID)
	notify(context.Background(), workspaceID, "test.synthetic.source", otherEventID)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, trigger.Shutdown(shutdownCtx))

	var queued int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM agent_runs WHERE workspace_id = ? AND agent_id = ?`,
		workspaceID, agent.AgentID,
	).Scan(&queued))
	require.Equal(t, 2, queued, "same event replay should dedupe while another event remains queued")

	// Construct a new queue object to model a worker process restart.
	restarted := agentruntime.NewMySQLQueue(testDB)
	run1, err := restarted.Claim(context.Background())
	require.NoError(t, err)
	run2, err := restarted.Claim(context.Background())
	require.NoError(t, err)
	for _, run := range []agentruntime.Run{run1, run2} {
		require.Equal(t, workspaceID, run.Job.WsID)
		require.Equal(t, agent.AgentID, run.Job.AgentID)
	}
	runsBySourceID := map[uint64]agentruntime.Run{
		run1.Job.SourceEventID: run1,
		run2.Job.SourceEventID: run2,
	}
	require.Contains(t, runsBySourceID, sourceEventID)
	require.Contains(t, runsBySourceID, otherEventID)
	require.NotEqual(t, run1.DedupeKey, run2.DedupeKey)

	claimed := runsBySourceID[sourceEventID]
	executor := &triggerCaptureExecutor{}
	runner := &agentruntime.OrchestratorRunner{
		DB:       testDB,
		Queries:  generated.New(testDB),
		Executor: executor,
	}
	require.NoError(t, runner.Run(context.Background(), claimed.Job, time.Now().UTC()))
	require.Equal(t, agentruntime.Trigger{
		EventType:    "test.synthetic.source",
		TaskPublicID: taskPublicID,
	}, executor.trigger, "source event context must reach prompt construction")

	started := selectEventsForTask(t, testDB, workspaceID, taskID, "ai.agent.run.started")
	completed := selectEventsForTask(t, testDB, workspaceID, taskID, "ai.agent.run.completed")
	require.Len(t, started, 1)
	require.Len(t, completed, 1)
	require.EqualValues(t, taskID, started[0].TaskID.Int32)
	require.EqualValues(t, taskID, completed[0].TaskID.Int32)
	memo := readAgentMemo(t, testDB, workspaceID, taskID)
	require.EqualValues(t, 1, asNumber(t, memo["attempts"]))
	require.Equal(t, "queued event reached the agent", memo["last_thought"])

	manualKey := fmt.Sprintf("%d:manual:%d", agent.AgentID, time.Now().UnixNano())
	require.NoError(t, restarted.Enqueue(context.Background(), agentruntime.Run{
		DedupeKey:   manualKey,
		Job:         agentruntime.Job{AgentID: agent.AgentID, WsID: workspaceID, DedupeKey: manualKey},
		ScheduledAt: time.Now().UTC(),
	}))
	manual, err := agentruntime.NewMySQLQueue(testDB).Claim(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(0), manual.Job.SourceEventID, "manual runs must retain the zero source ID")
	var storedSourceID sql.NullInt64
	require.NoError(t, testDB.QueryRow(`SELECT source_event_id FROM agent_runs WHERE dedupe_key = ?`, manualKey).Scan(&storedSourceID))
	require.False(t, storedSourceID.Valid, "zero source IDs must be stored as SQL NULL")
}
