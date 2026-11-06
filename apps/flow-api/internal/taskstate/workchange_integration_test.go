package taskstate_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var workChangeShared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "taskstate_work_change_test"})

type reviewedWorkFixture struct {
	workspaceID       uint32
	workspacePublicID types.PublicID
	projectID         uint32
	taskID            uint32
	taskPublicID      types.PublicID
	userID            uint32
	agentID           uint32
}

func workChangeDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := workChangeShared.Start(context.Background())
	require.NoError(t, err, "start taskstate MySQL")
	return inst.DB
}

func seedReviewedWork(t *testing.T, db *sql.DB, completionPolicy string) reviewedWorkFixture {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	workspacePublicID := types.New()
	taskPublicID := types.New()
	insertID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec: %s", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- test fixture IDs are INT UNSIGNED
	}

	workspaceID := insertID(`INSERT INTO workspaces (public_id, slug, name, timezone)
VALUES (?, ?, ?, 'UTC')`, workspacePublicID, "twc-"+suffix, "Taskstate Work Change")
	userID := insertID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, types.New(), "taskstate+"+suffix+"@example.test", "Work Change Reviewer")
	_, err := db.ExecContext(ctx, `INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
VALUES (?, ?, ?, 'owner')`, types.New(), workspaceID, userID)
	require.NoError(t, err)
	projectID := insertID(`INSERT INTO projects (public_id, workspace_id, name, slug)
VALUES (?, ?, ?, ?)`, types.New(), workspaceID, "Taskstate Work Project", "twcp-"+suffix)
	taskID := insertID(`INSERT INTO tasks (
  public_id, workspace_id, project_id, created_by_user_id, task_number,
  title, derived_state, completion_policy, work_revision
) VALUES (?, ?, ?, ?, 1, 'Reviewed work', 'done', ?, 1)`,
		taskPublicID, workspaceID, projectID, userID, completionPolicy)

	// Give the fixture a current approved review so the content change is
	// demonstrably invalidating completed, reviewed work.
	submissionID := insertID(`INSERT INTO task_submissions (
  public_id, workspace_id, task_id, work_revision, submitted_by_user_id,
  reviewer_user_id, acceptance_criteria_json, evidence_urls_json, attachment_digests_json
) VALUES (?, ?, ?, 1, ?, ?, JSON_ARRAY(), JSON_ARRAY(), JSON_ARRAY())`,
		types.New(), workspaceID, taskID, userID, userID)
	_, err = db.ExecContext(ctx, `INSERT INTO task_review_decisions (
  public_id, workspace_id, task_id, submission_id, decided_by_user_id, decision
) VALUES (?, ?, ?, ?, ?, 'approved')`, types.New(), workspaceID, taskID, submissionID, userID)
	require.NoError(t, err)

	agent := helpers.SeedAgent(t, db, workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Work Change Agent"})
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, workspacePublicID.String()) })
	return reviewedWorkFixture{
		workspaceID:       workspaceID,
		workspacePublicID: workspacePublicID,
		projectID:         projectID,
		taskID:            taskID,
		taskPublicID:      taskPublicID,
		userID:            userID,
		agentID:           agent.AgentID,
	}
}

// changeTitleAsAgent models a Patch caller: it reads the task under lock,
// compares effective content, invokes the helper only on an actual change,
// then writes content in the same transaction.
func changeTitleAsAgent(db *sql.DB, f reviewedWorkFixture, title string) error {
	ctx := context.Background()
	agentID := int64(f.agentID)
	return dbretry.InTx(ctx, db, "taskstate_test.change_title", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var currentTitle string
		if err := tx.RawTx().QueryRowContext(ctx, `SELECT title FROM tasks
WHERE workspace_id = ? AND id = ? AND enabled = TRUE LIMIT 1 FOR UPDATE`,
			f.workspaceID, f.taskID).Scan(&currentTitle); err != nil {
			return err
		}
		if currentTitle == title {
			return nil
		}
		spec, err := taskstate.PrepareWorkChangeTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID:  f.workspaceID,
			TaskID:       f.taskID,
			PublicID:     f.taskPublicID,
			ActorAgentID: &agentID,
			Via:          "integration_test",
		})
		if err != nil {
			return err
		}
		if spec != nil {
			return fmt.Errorf("prepare work change rejected: %v", spec)
		}
		_, err = tx.RawTx().ExecContext(ctx, `UPDATE tasks SET title = ?, updated_by_user_id = NULL
WHERE workspace_id = ? AND id = ? AND enabled = TRUE`, title, f.workspaceID, f.taskID)
		return err
	})
}

func TestReviewedDoneEffectiveAgentContentChangeReopensAndInvalidatesApproval(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	ctx := context.Background()
	queries := generated.New(db)
	canComplete, err := queries.CanCompleteTask(ctx, generated.CanCompleteTaskParams{WorkspaceID: f.workspaceID, ID: f.taskID})
	require.NoError(t, err)
	require.Equal(t, int32(1), canComplete, "fixture starts as currently reviewed work")

	require.NoError(t, changeTitleAsAgent(db, f, "Updated reviewed work"))

	var state string
	var workRevision uint32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT derived_state, work_revision FROM tasks
WHERE workspace_id = ? AND id = ?`, f.workspaceID, f.taskID).Scan(&state, &workRevision))
	require.Equal(t, "waiting", state, "effective content changes reopen review-required Done work")
	require.Equal(t, uint32(2), workRevision, "content revision remains trigger-owned")
	canComplete, err = queries.CanCompleteTask(ctx, generated.CanCompleteTaskParams{WorkspaceID: f.workspaceID, ID: f.taskID})
	require.NoError(t, err)
	require.Zero(t, canComplete, "the previous approval cannot approve the new revision")

	var actorUser, actorAgent sql.NullInt32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_user_id, actor_agent_id FROM events
WHERE workspace_id = ? AND task_id = ? AND type = 'task.transition.reopen'
ORDER BY id DESC LIMIT 1`, f.workspaceID, f.taskID).Scan(&actorUser, &actorAgent))
	require.False(t, actorUser.Valid, "agent transition must not name a delegator as author")
	require.True(t, actorAgent.Valid)
	require.Equal(t, int64(f.agentID), int64(actorAgent.Int32))
}

func TestReviewedDoneNoOpAndManualContentChangeStayDone(t *testing.T) {
	db := workChangeDB(t)
	ctx := context.Background()

	t.Run("review-required no-op", func(t *testing.T) {
		f := seedReviewedWork(t, db, "review_required")
		before := countReopenEvents(t, db, f)
		require.NoError(t, changeTitleAsAgent(db, f, "Reviewed work"))
		assertDoneWithoutNewReopen(t, db, f, before)
	})

	t.Run("manual effective content change", func(t *testing.T) {
		f := seedReviewedWork(t, db, "manual")
		before := countReopenEvents(t, db, f)
		require.NoError(t, changeTitleAsAgent(db, f, "Updated manual work"))
		assertDoneWithoutNewReopen(t, db, f, before)
		var title string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT title FROM tasks WHERE workspace_id = ? AND id = ?`,
			f.workspaceID, f.taskID).Scan(&title))
		require.Equal(t, "Updated manual work", title)
	})
}

func countReopenEvents(t *testing.T, db *sql.DB, f reviewedWorkFixture) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events
WHERE workspace_id = ? AND task_id = ? AND type = 'task.transition.reopen'`, f.workspaceID, f.taskID).Scan(&count))
	return count
}

func assertDoneWithoutNewReopen(t *testing.T, db *sql.DB, f reviewedWorkFixture, before int) {
	t.Helper()
	var state string
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT derived_state FROM tasks
WHERE workspace_id = ? AND id = ?`, f.workspaceID, f.taskID).Scan(&state))
	require.Equal(t, "done", state)
	require.Equal(t, before, countReopenEvents(t, db, f))
}
