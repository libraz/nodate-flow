package sqlviews

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestProposalCacheAssigneeFollowsTaskListRule pins the cache snapshot to
// the task list's primary assignee: the first assignee in display order,
// and none when that actor is an agent, even if a human assignee follows.
func TestProposalCacheAssigneeFollowsTaskListRule(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })
	var task struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Agent-first assignee",
	}, &task)
	taskPublicID, err := types.Parse(task.ID)
	require.NoError(t, err)

	wsID, ok := helpers.WorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	require.True(t, ok)
	var taskID, userID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = ?`, taskPublicID).Scan(&taskID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, tt.UserPublicID).Scan(&userID))
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Name: "First Assignee Agent"})

	_, err = testDB.ExecContext(ctx, `INSERT IGNORE INTO task_actors
  (public_id, workspace_id, task_id, user_id, kind, role, sort_weight) VALUES (?, ?, ?, ?, 'user', 'assignee', 5)`,
		types.New(), wsID, taskID, userID)
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx, `INSERT INTO task_actors
  (public_id, workspace_id, task_id, agent_id, kind, role, sort_weight) VALUES (?, ?, ?, ?, 'agent', 'assignee', -1)`,
		types.New(), wsID, taskID, agent.AgentID)
	require.NoError(t, err)

	rows, err := generated.New(testDB).ListProposalCacheTaskInputs(ctx, generated.ListProposalCacheTaskInputsParams{
		WorkspaceID: wsID,
		ActorUserID: int64(userID),
		PublicIds:   []types.PublicID{taskPublicID},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].AssigneeName.Valid, "an agent first in display order means no primary assignee")
	require.Contains(t, []any{"", []byte("")}, rows[0].AssigneePublicID, "no primary assignee public id")
}
