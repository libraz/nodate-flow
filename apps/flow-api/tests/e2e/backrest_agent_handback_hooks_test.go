package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// hookCounter counts the event kinds the eventbus notify hooks see for
// one workspace.
func hookCounter(t *testing.T, wsID uint32) func(kind string) int {
	t.Helper()
	var (
		mu   sync.Mutex
		seen = map[string]int{}
	)
	handle := eventbus.AddNotifyHook(func(_ context.Context, ws uint32, kind string, _ uint64) {
		if ws != wsID {
			return
		}
		mu.Lock()
		seen[kind]++
		mu.Unlock()
	})
	t.Cleanup(func() { eventbus.RemoveNotifyHook(handle) })
	return func(kind string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[kind]
	}
}

// TestAgentRuntimeHandBackReachesTheEventbusHooks holds the agent
// runtime to the shared recorder: a low-confidence run's hand-back and
// its run events fan out to the notify hooks, which is how the human
// assignee, webhooks and SSE hear about them, and the hand-back leaves
// the same audit action the REST handoff does.
func TestAgentRuntimeHandBackReachesTheEventbusHooks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Runtime hand-back fan-out")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	seen := hookCounter(t, wsID)

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

	require.Eventually(t, func() bool {
		return seen("agent.task.handoff_to_user") == 1 &&
			seen("ai.agent.run.started") == 1 &&
			seen("ai.agent.run.completed") == 1
	}, 5*time.Second, 50*time.Millisecond, "the runtime's hand-back and run events must reach the eventbus hooks")
	require.Equal(t, 1, auditCount(t, "task.handoff.to_user", taskID))
}

// TestAutoActionHandBackReachesTheEventbusHooks is the same claim for
// the auto-action executor's stuck-agent hand-back.
func TestAutoActionHandBackReachesTheEventbusHooks(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	lockAutoActionPass(t)

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Auto-action hand-back fan-out")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	seen := hookCounter(t, wsID)

	helpers.SetTaskAgentMemo(t, testDB, tt.WorkspacePublicID, taskID, map[string]any{
		"attempts":         5,
		"last_finished_at": time.Now().Add(-12 * time.Hour).Unix(),
	})
	ageTaskUpdatedAt(t, testDB, wsID, taskInternalID, 12*time.Hour)

	exec := &autoactions.Executor{
		DB:     testDB,
		Logger: testLogger(t),
		Config: autoactions.ExecutorConfig{ConfidenceThreshold: 0.5},
	}
	requireCompletePass(context.Background(), t, exec)

	require.Eventually(t, func() bool {
		return seen("agent.task.handoff_to_user") == 1
	}, 5*time.Second, 50*time.Millisecond, "the auto-action hand-back must reach the eventbus hooks")
	require.Equal(t, 1, auditCount(t, "task.handoff.to_user", taskID))
}
