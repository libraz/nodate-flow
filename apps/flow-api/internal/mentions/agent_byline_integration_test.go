package mentions_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mentions"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestAgentMentionKeepsBylineAfterAgentDeletion keeps the agent that wrote a
// mention readable once the agent row is gone and the FK has cleared.
func TestAgentMentionKeepsBylineAfterAgentDeletion(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	db := inst.DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	suffix = suffix[len(suffix)-12:]
	execID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec %q", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		require.True(t, id > 0 && id <= math.MaxUint32, "seed id %d", id)
		return uint32(id) //#nosec G115 -- bounds checked immediately above
	}
	workspacePublicID := types.New()
	mentionedPublicID := types.New()
	taskPublicID := types.New()
	workspaceID := execID(`INSERT INTO workspaces (public_id, slug, name, timezone)
VALUES (?, ?, ?, 'UTC')`, workspacePublicID, "mb-"+suffix, "Mention byline "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, workspacePublicID.String()) })
	mentionedID := execID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, mentionedPublicID, "mention-byline+"+suffix+"@example.test", "Mentioned "+suffix)
	_, err = db.ExecContext(ctx, `INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
VALUES (?, ?, ?, 'owner', NOW())`, types.New(), workspaceID, mentionedID)
	require.NoError(t, err)
	projectID := execID(`INSERT INTO projects (public_id, workspace_id, name, slug)
VALUES (?, ?, ?, ?)`, types.New(), workspaceID, "Mention byline "+suffix, "mb-"+suffix)
	taskID := execID(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility)
VALUES (?, ?, ?, ?, 1, 'Mention byline', 1, 'public')`, taskPublicID, workspaceID, projectID, mentionedID)
	agent := helpers.SeedAgent(t, db, workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Byline Agent " + suffix})

	agentID := int64(agent.AgentID)
	require.NoError(t, dbretry.InTx(ctx, db, "mentions.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return mentions.SyncTaskDescription(ctx, tx, mentions.TaskDescriptionArgs{
			WorkspaceID:  workspaceID,
			TaskID:       taskID,
			TaskPublicID: taskPublicID,
			ActorAgentID: &agentID,
			Body:         "Please look @[Mentioned](user:" + mentionedPublicID.String() + ")",
		})
	}))

	_, err = db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id = ?`, agent.AgentID)
	require.NoError(t, err, "delete agent")
	var actorAgentID sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_agent_id FROM mentions WHERE workspace_id = ? AND task_id = ?`,
		workspaceID, taskID).Scan(&actorAgentID))
	require.False(t, actorAgentID.Valid, "the FK clears when the agent is deleted")

	q := generated.New(db)
	byTask, err := q.ListMentionsForTask(ctx, generated.ListMentionsForTaskParams{
		TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- seeded id fits int32
		WorkspaceID: workspaceID,
		Limit:       10,
	})
	require.NoError(t, err)
	require.Len(t, byTask, 1)
	require.Equal(t, agent.AgentPublicID, fmt.Sprintf("%s", byTask[0].ActorAgentPublicID))
	require.Equal(t, agent.Name, byTask[0].ActorAgentName)

	byUser, err := q.ListMentionsForUser(ctx, generated.ListMentionsForUserParams{
		WorkspaceID:     workspaceID,
		MentionedUserID: mentionedID,
		Limit:           10,
	})
	require.NoError(t, err)
	require.Len(t, byUser, 1)
	require.Equal(t, agent.AgentPublicID, fmt.Sprintf("%s", byUser[0].ActorAgentPublicID))
	require.Equal(t, agent.Name, byUser[0].ActorAgentName)
}
