package taskservice_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type serviceFixture struct {
	workspacePublicID types.PublicID
	workspaceID       uint32
	userID            uint32
	userPublicID      types.PublicID
	projectID         uint32
	taskID            uint32
	taskPublicID      types.PublicID
	agentID           uint32
	agentPublicID     types.PublicID
	agentName         string
}

func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	require.NotNil(t, inst)
	return inst.DB
}

func seedServiceFixture(t *testing.T, db *sql.DB, withAgent bool) serviceFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(suffix) > 12 {
		suffix = suffix[len(suffix)-12:]
	}
	f := serviceFixture{workspacePublicID: types.New(), userPublicID: types.New(), taskPublicID: types.New()}
	execID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec %q", query)
		id, err := res.LastInsertId()
		require.NoError(t, err, "seed last insert id")
		if id <= 0 || id > math.MaxUint32 {
			t.Fatalf("seed id %d does not fit uint32", id)
		}
		return uint32(id) //#nosec G115 -- the fixture checks positive uint32 bounds immediately above; testing.Fatal terminates the failing path.
	}

	f.workspaceID = execID(`INSERT INTO workspaces (public_id, slug, name, timezone)
VALUES (?, ?, ?, 'UTC')`, f.workspacePublicID, "ts-"+suffix, "Taskservice "+suffix)
	f.userID = execID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, f.userPublicID, "taskservice+"+suffix+"@example.test", "Taskservice Human "+suffix)
	_, err := db.ExecContext(ctx, `INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
VALUES (?, ?, ?, 'owner', NOW())`, types.New(), f.workspaceID, f.userID)
	require.NoError(t, err, "seed workspace owner")
	f.projectID = execID(`INSERT INTO projects (public_id, workspace_id, name, slug)
VALUES (?, ?, ?, ?)`, types.New(), f.workspaceID, "Taskservice Project "+suffix, "tp-"+suffix)
	f.taskID = execID(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility)
VALUES (?, ?, ?, ?, 1, 'Taskservice fixture', 1, 'public')`, f.taskPublicID, f.workspaceID, f.projectID, f.userID)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, f.workspacePublicID.String()) })

	if withAgent {
		agent := helpers.SeedAgent(t, db, f.workspacePublicID.String(), helpers.SeedAgentOptions{Name: "Taskservice Agent " + suffix})
		f.agentID = agent.AgentID
		f.agentPublicID, err = types.Parse(agent.AgentPublicID)
		require.NoError(t, err)
		f.agentName = agent.Name
		_, err = db.ExecContext(ctx, `UPDATE ai_agents
SET tools_json = JSON_OBJECT('set_priority','auto','add_comment','auto'),
    allowed_scopes_json = JSON_ARRAY('write:workspace')
WHERE id = ?`, f.agentID)
		require.NoError(t, err, "allow the task commands the fixture agent exercises")
		helpers.AssignAgentToTask(t, db, f.workspacePublicID.String(), f.taskPublicID.String(), f.agentID)
	}
	return f
}

func serviceFor(db *sql.DB) *taskservice.Service {
	queries := generated.New(db)
	return taskservice.New(db, queries, mutationlog.New(db, queries))
}

func agentPrincipal(t *testing.T, f serviceFixture, targets ...types.PublicID) execution.Principal {
	t.Helper()
	principal, err := execution.NewAgentPrincipal(
		f.workspaceID,
		f.agentID,
		[]string{execution.ScopeWriteWorkspace},
		[]string{execution.ToolReadTask, execution.ToolSetPriority, execution.ToolAddComment},
		targets,
		0,
	)
	require.NoError(t, err)
	return principal
}

func TestAgentTaskCommandsAuthorizeAndAttributeRealAgent(t *testing.T) {
	db := integrationDB(t)
	f := seedServiceFixture(t, db, true)
	ctx := context.Background()
	svc := serviceFor(db)
	agent := agentPrincipal(t, f, f.taskPublicID)

	read, err := svc.Read(ctx, agent, f.taskPublicID)
	require.NoError(t, err)
	require.Equal(t, f.taskPublicID, read.ID)
	require.Equal(t, "Taskservice fixture", read.Title)

	updated, err := svc.SetPriority(ctx, agent, f.taskPublicID, 4)
	require.NoError(t, err)
	require.Equal(t, int32(4), updated.Priority)

	body := fmt.Sprintf("Review @[Taskservice Human](user:%s)", f.userPublicID.String())
	comment, err := svc.AddComment(ctx, agent, f.taskPublicID, body)
	require.NoError(t, err)
	require.Equal(t, taskservice.AuthorKindAgent, comment.AuthorKind)
	require.Empty(t, comment.AuthorID, "an agent comment must not masquerade as its token owner")
	require.Equal(t, f.agentPublicID.String(), comment.AuthorAgentID)
	require.Equal(t, f.agentName, comment.AuthorDisplayName)
	require.NotEqual(t, f.taskPublicID, comment.ID)

	var (
		authorID      sql.NullInt32
		authorAgentID sql.NullInt32
		authorKind    string
	)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT author_id, author_agent_id, author_kind
FROM comments WHERE workspace_id = ? AND public_id = ?`, f.workspaceID, comment.ID).
		Scan(&authorID, &authorAgentID, &authorKind))
	require.False(t, authorID.Valid, "agent comments leave the human author FK NULL")
	require.True(t, authorAgentID.Valid)
	require.Equal(t, int64(f.agentID), int64(authorAgentID.Int32))
	require.Equal(t, taskservice.AuthorKindAgent, authorKind)

	var (
		viewKind    string
		viewName    string
		viewAgentID string
	)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT author_kind, author_display_name, author_agent_public_id
FROM v_comment_for_task WHERE workspace_id = ? AND public_id = ?`, f.workspaceID, comment.ID).
		Scan(&viewKind, &viewName, &viewAgentID))
	require.Equal(t, taskservice.AuthorKindAgent, viewKind)
	require.Equal(t, f.agentName, viewName)
	require.Equal(t, f.agentPublicID.String(), viewAgentID)

	// Agent comments never mention: an agent cannot page humans by writing @name.
	var mentionCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mentions
WHERE workspace_id = ? AND comment_id = (SELECT id FROM comments WHERE public_id = ?)`, f.workspaceID, comment.ID).
		Scan(&mentionCount))
	require.Zero(t, mentionCount)

	for _, eventType := range []string{"task.updated", "task.comment.added"} {
		var userActor, agentActor sql.NullInt32
		require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_user_id, actor_agent_id FROM events
WHERE workspace_id = ? AND task_id = ? AND type = ? ORDER BY id DESC LIMIT 1`, f.workspaceID, f.taskID, eventType).
			Scan(&userActor, &agentActor), "event type %s", eventType)
		require.False(t, userActor.Valid, "agent event %s must not use a human author", eventType)
		require.True(t, agentActor.Valid, "event type %s must carry agent attribution", eventType)
		require.Equal(t, int64(f.agentID), int64(agentActor.Int32))
	}

	var auditActor sql.NullInt32
	var auditMetadata []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT actor_user_id, metadata_json FROM audit_logs
WHERE workspace_id = ? AND action = 'comment.create' AND resource_public_id = ?
ORDER BY id DESC LIMIT 1`, f.workspaceID, comment.ID).Scan(&auditActor, &auditMetadata))
	require.False(t, auditActor.Valid, "agent audit rows must not use a human actor")
	require.Contains(t, string(auditMetadata), f.agentPublicID.String())

	// The historical byline survives agent deletion: the FK clears through
	// ON DELETE SET NULL while the snapshotted public id and name remain.
	_, err = db.ExecContext(ctx, `DELETE FROM ai_agents WHERE id = ?`, f.agentID)
	require.NoError(t, err, "delete agent")
	var afterDeleteKind string
	var afterDeleteName string
	var afterDeleteAgent string
	var afterDeleteBody string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT author_kind, author_display_name, author_agent_public_id, body
FROM v_comment_for_task WHERE workspace_id = ? AND public_id = ?`, f.workspaceID, comment.ID).
		Scan(&afterDeleteKind, &afterDeleteName, &afterDeleteAgent, &afterDeleteBody))
	require.Equal(t, taskservice.AuthorKindAgent, afterDeleteKind)
	require.Equal(t, f.agentName, afterDeleteName)
	require.Equal(t, f.agentPublicID.String(), afterDeleteAgent)
	require.Equal(t, body, afterDeleteBody)
	var deletedAuthorAgentID sql.NullInt32
	var retainedAuthorKind string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT author_agent_id, author_kind FROM comments
WHERE workspace_id = ? AND public_id = ?`, f.workspaceID, comment.ID).
		Scan(&deletedAuthorAgentID, &retainedAuthorKind))
	require.False(t, deletedAuthorAgentID.Valid)
	require.Equal(t, taskservice.AuthorKindAgent, retainedAuthorKind)
}

func TestHumanPriorityAndCommentCompatibility(t *testing.T) {
	db := integrationDB(t)
	f := seedServiceFixture(t, db, false)
	principal, err := execution.NewUserPrincipal(f.workspaceID, f.userID)
	require.NoError(t, err)
	svc := serviceFor(db)

	updated, err := svc.SetPriority(context.Background(), principal, f.taskPublicID, 3)
	require.NoError(t, err)
	require.Equal(t, int32(3), updated.Priority)
	comment, err := svc.AddComment(context.Background(), principal, f.taskPublicID, "Human comment")
	require.NoError(t, err)
	require.Equal(t, taskservice.AuthorKindUser, comment.AuthorKind)
	require.Equal(t, f.userPublicID.String(), comment.AuthorID)
	require.Empty(t, comment.AuthorAgentID)

	var (
		authorID      sql.NullInt32
		authorAgentID sql.NullInt32
		authorKind    string
	)
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT author_id, author_agent_id, author_kind
FROM comments WHERE workspace_id = ? AND public_id = ?`, f.workspaceID, comment.ID).
		Scan(&authorID, &authorAgentID, &authorKind))
	require.True(t, authorID.Valid)
	require.Equal(t, int64(f.userID), int64(authorID.Int32))
	require.False(t, authorAgentID.Valid)
	require.Equal(t, taskservice.AuthorKindUser, authorKind)
}

func TestAgentCommentGETBylineSurvivesAgentDeletion(t *testing.T) {
	db := integrationDB(t)
	srv := helpers.StartTestServer(t, db)
	tenant := helpers.CreateTestTenant(t, srv.BaseURL)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, tenant.WorkspacePublicID) })

	var task struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, srv.BaseURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId":  tenant.ProjectPublicID,
		"title":      "Agent comment byline",
		"visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID)
	taskPublicID, err := types.Parse(task.ID)
	require.NoError(t, err)
	workspaceID, ok := helpers.WorkspaceInternalID(t, db, tenant.WorkspacePublicID)
	require.True(t, ok)

	seededAgent := helpers.SeedAgent(t, db, tenant.WorkspacePublicID, helpers.SeedAgentOptions{
		Name: "Byline Agent",
	})
	_, err = db.ExecContext(context.Background(), `UPDATE ai_agents
SET tools_json = JSON_OBJECT('add_comment','auto'), allowed_scopes_json = JSON_ARRAY('write:workspace')
WHERE id = ?`, seededAgent.AgentID)
	require.NoError(t, err)
	helpers.AssignAgentToTask(t, db, tenant.WorkspacePublicID, task.ID, seededAgent.AgentID)

	principal, err := execution.NewAgentPrincipal(
		workspaceID,
		seededAgent.AgentID,
		[]string{execution.ScopeWriteWorkspace},
		[]string{execution.ToolAddComment},
		[]types.PublicID{taskPublicID},
		0,
	)
	require.NoError(t, err)
	comment, err := serviceFor(db).AddComment(context.Background(), principal, taskPublicID, "Agent-authored comment")
	require.NoError(t, err)

	type commentRow struct {
		ID                string `json:"id"`
		AuthorID          string `json:"authorId"`
		AuthorKind        string `json:"authorKind"`
		AuthorAgentID     string `json:"authorAgentId"`
		AuthorDisplayName string `json:"authorDisplayName"`
		Body              string `json:"body"`
	}
	readComments := func() []commentRow {
		var response struct {
			Comments []commentRow `json:"comments"`
		}
		helpers.DoJSON(t, http.MethodGet, srv.BaseURL+"/tasks/"+task.ID+"/comments", tenant.AccessToken, nil, &response)
		return response.Comments
	}

	comments := readComments()
	require.Len(t, comments, 1)
	require.Equal(t, comment.ID.String(), comments[0].ID)
	require.Equal(t, taskservice.AuthorKindAgent, comments[0].AuthorKind)
	require.Empty(t, comments[0].AuthorID, "the agent byline must not be a user")
	require.Equal(t, seededAgent.AgentPublicID, comments[0].AuthorAgentID)
	require.Equal(t, seededAgent.Name, comments[0].AuthorDisplayName)

	_, err = db.ExecContext(context.Background(), `DELETE FROM ai_agents WHERE id = ?`, seededAgent.AgentID)
	require.NoError(t, err)
	comments = readComments()
	require.Len(t, comments, 1)
	require.Equal(t, taskservice.AuthorKindAgent, comments[0].AuthorKind)
	require.Empty(t, comments[0].AuthorID)
	require.Equal(t, seededAgent.AgentPublicID, comments[0].AuthorAgentID)
	require.Equal(t, seededAgent.Name, comments[0].AuthorDisplayName)
	require.Equal(t, "Agent-authored comment", comments[0].Body)
}

func TestAgentRevocationAndTargetBoundaries(t *testing.T) {
	db := integrationDB(t)
	f := seedServiceFixture(t, db, true)
	ctx := context.Background()
	svc := serviceFor(db)
	principal := agentPrincipal(t, f, f.taskPublicID)

	_, err := db.ExecContext(ctx, `UPDATE ai_agents SET paused = TRUE WHERE id = ?`, f.agentID)
	require.NoError(t, err)
	_, err = svc.Read(ctx, principal, f.taskPublicID)
	require.ErrorIs(t, err, apierrors.New(apierrors.WsTaskNotFound), "paused agent")
	_, err = db.ExecContext(ctx, `UPDATE ai_agents SET paused = FALSE WHERE id = ?`, f.agentID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE task_actors SET enabled = FALSE
WHERE task_id = ? AND agent_id = ? AND kind = 'agent' AND role = 'assignee'`, f.taskID, f.agentID)
	require.NoError(t, err)
	_, err = svc.Read(ctx, principal, f.taskPublicID)
	require.ErrorIs(t, err, apierrors.New(apierrors.WsTaskNotFound), "unassigned agent")
	_, err = db.ExecContext(ctx, `UPDATE task_actors SET enabled = TRUE
WHERE task_id = ? AND agent_id = ? AND kind = 'agent' AND role = 'assignee'`, f.taskID, f.agentID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE tasks SET visibility = 'private' WHERE id = ?`, f.taskID)
	require.NoError(t, err)
	_, err = svc.Read(ctx, principal, f.taskPublicID)
	require.ErrorIs(t, err, apierrors.New(apierrors.WsTaskNotFound), "private task")
	_, err = db.ExecContext(ctx, `UPDATE tasks SET visibility = 'public' WHERE id = ?`, f.taskID)
	require.NoError(t, err)

	other := types.New()
	_, err = db.ExecContext(ctx, `INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility)
VALUES (?, ?, ?, 2, 'Uncaptured task', 'public')`, other, f.workspaceID, f.projectID)
	require.NoError(t, err)
	_, err = svc.Read(ctx, principal, other)
	require.ErrorIs(t, err, apierrors.New(apierrors.WsTaskNotFound), "target outside immutable captured set")
}

func TestAgentCannotReadCapturedTaskFromAnotherWorkspace(t *testing.T) {
	db := integrationDB(t)
	own := seedServiceFixture(t, db, true)
	foreign := seedServiceFixture(t, db, false)
	principal := agentPrincipal(t, own, own.taskPublicID, foreign.taskPublicID)

	_, err := serviceFor(db).Read(context.Background(), principal, foreign.taskPublicID)
	require.ErrorIs(t, err, execution.ErrTaskOutsideWorkspace)
}

func TestAgentRespectsTokenWorkspaceBinding(t *testing.T) {
	db := integrationDB(t)
	workspaceA := seedServiceFixture(t, db, false)
	workspaceB := seedServiceFixture(t, db, true)
	principal := agentPrincipal(t, workspaceB, workspaceB.taskPublicID)
	svc := serviceFor(db)

	ctx := authn.WithTokenKind(context.Background(), authn.TokenKindMCP)
	ctx = authn.WithTokenWorkspaceID(ctx, workspaceA.workspaceID)

	_, err := svc.Read(ctx, principal, workspaceB.taskPublicID)
	require.ErrorIs(t, err, apierrors.New(apierrors.McpTokenWorkspaceMismatch),
		"a workspace-A bearer token must not use a valid agent principal and assigned task from workspace B")
	_, err = svc.SetPriority(ctx, principal, workspaceB.taskPublicID, 4)
	require.ErrorIs(t, err, apierrors.New(apierrors.McpTokenWorkspaceMismatch))
	_, err = svc.AddComment(ctx, principal, workspaceB.taskPublicID, "must not be written")
	require.ErrorIs(t, err, apierrors.New(apierrors.McpTokenWorkspaceMismatch))

	var priority int32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT priority FROM tasks WHERE id = ?`, workspaceB.taskID).Scan(&priority))
	require.Equal(t, int32(1), priority, "workspace binding must prevent priority writes")
	var comments int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE task_id = ?`, workspaceB.taskID).Scan(&comments))
	require.Zero(t, comments, "workspace binding must prevent comment writes")
}

func TestDisabledParentsDenyAgentTaskOperations(t *testing.T) {
	db := integrationDB(t)
	for _, parent := range []string{"workspace", "project"} {
		t.Run(parent, func(t *testing.T) {
			f := seedServiceFixture(t, db, true)
			ctx := context.Background()
			if parent == "workspace" {
				_, err := db.ExecContext(ctx, `UPDATE workspaces SET enabled = FALSE WHERE id = ?`, f.workspaceID)
				require.NoError(t, err)
			} else {
				_, err := db.ExecContext(ctx, `UPDATE projects SET enabled = FALSE WHERE id = ?`, f.projectID)
				require.NoError(t, err)
			}

			svc := serviceFor(db)
			principal := agentPrincipal(t, f, f.taskPublicID)
			_, err := svc.Read(ctx, principal, f.taskPublicID)
			require.Error(t, err, "Read must reject a task with a disabled %s", parent)
			_, err = svc.SetPriority(ctx, principal, f.taskPublicID, 4)
			require.Error(t, err, "SetPriority must reject a task with a disabled %s", parent)
			_, err = svc.AddComment(ctx, principal, f.taskPublicID, "must not be written")
			require.Error(t, err, "AddComment must reject a task with a disabled %s", parent)

			var priority int32
			require.NoError(t, db.QueryRowContext(ctx, `SELECT priority FROM tasks WHERE id = ?`, f.taskID).Scan(&priority))
			require.Equal(t, int32(1), priority, "disabled %s must prevent priority writes", parent)
			var comments int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE task_id = ?`, f.taskID).Scan(&comments))
			require.Zero(t, comments, "disabled %s must prevent comment writes", parent)
			var events int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events
WHERE task_id = ? AND type IN ('task.updated', 'task.comment.added')`, f.taskID).Scan(&events))
			require.Zero(t, events, "disabled %s must not append mutation events", parent)
		})
	}
}

func TestWorkspaceRevocationWhileWaitingForTaskLock(t *testing.T) {
	db := integrationDB(t)
	f := seedServiceFixture(t, db, false)
	principal, err := execution.NewUserPrincipal(f.workspaceID, f.userID)
	require.NoError(t, err)
	svc := serviceFor(db)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	lockConn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer lockConn.Close()
	lockTx, err := lockConn.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback() }()
	var lockedTaskID uint32
	require.NoError(t, lockTx.QueryRowContext(ctx, `SELECT id FROM tasks
WHERE workspace_id = ? AND public_id = ? FOR UPDATE`, f.workspaceID, f.taskPublicID).Scan(&lockedTaskID))
	require.Equal(t, f.taskID, lockedTaskID)

	type result struct {
		task taskservice.Task
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		updated, err := svc.SetPriority(ctx, principal, f.taskPublicID, 4)
		completed <- result{task: updated, err: err}
	}()

	// Observe the service's blocked task-locking read in SHOW FULL PROCESSLIST
	// before changing the authority row. MySQL permits users to see their own
	// connections without PROCESS, and all test connections use this account.
	// The held row lock makes the matching SELECT stay visible until release.
	waitDeadline := time.Now().Add(5 * time.Second)
	observedWait := false
	for time.Now().Before(waitDeadline) {
		rows, err := db.QueryContext(ctx, `SHOW FULL PROCESSLIST`)
		require.NoError(t, err, "observe task lock wait")
		for rows.Next() {
			var (
				id      uint64
				user    sql.NullString
				host    sql.NullString
				dbName  sql.NullString
				command sql.NullString
				seconds sql.NullInt64
				state   sql.NullString
				info    sql.NullString
			)
			if err := rows.Scan(&id, &user, &host, &dbName, &command, &seconds, &state, &info); err != nil {
				_ = rows.Close()
				require.NoError(t, err, "scan process list")
			}
			query := strings.ToUpper(info.String)
			if strings.Contains(query, "SELECT ID, WORKSPACE_ID, PROJECT_ID, VISIBILITY, CREATED_BY_USER_ID FROM TASKS") &&
				strings.Contains(query, "FOR UPDATE") && seconds.Valid && seconds.Int64 > 0 {
				observedWait = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			require.NoError(t, err, "read process list")
		}
		require.NoError(t, rows.Close(), "close process list")
		if observedWait {
			break
		}
		runtime.Gosched()
	}
	require.True(t, observedWait, "service command did not expose its blocked task-locking read")

	_, err = lockTx.ExecContext(ctx, `UPDATE workspace_members SET enabled = FALSE
WHERE workspace_id = ? AND user_id = ?`, f.workspaceID, f.userID)
	require.NoError(t, err)
	require.NoError(t, lockTx.Commit())

	var got result
	select {
	case got = <-completed:
	case <-ctx.Done():
		t.Fatalf("service did not finish after task lock release: %v", ctx.Err())
	}
	require.ErrorIs(t, got.err, apierrors.New(apierrors.WsTaskAccessDenied), "the current membership must be checked after the task wait")

	var priority int32
	require.NoError(t, db.QueryRowContext(ctx, `SELECT priority FROM tasks WHERE id = ?`, f.taskID).Scan(&priority))
	require.Equal(t, int32(1), priority, "revoked command must not write")
	var updates int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events
WHERE workspace_id = ? AND task_id = ? AND type = 'task.updated'`, f.workspaceID, f.taskID).Scan(&updates))
	require.Zero(t, updates, "revoked command must not append a mutation event")
}

func TestServiceRejectsInvalidCommandInput(t *testing.T) {
	var svc *taskservice.Service
	if _, err := svc.SetPriority(context.Background(), execution.Principal{}, types.PublicID{}, 5); !errors.Is(err, taskservice.ErrPriorityOutOfRange) {
		t.Fatalf("SetPriority error = %v, want ErrPriorityOutOfRange", err)
	}
	if _, err := svc.AddComment(context.Background(), execution.Principal{}, types.PublicID{}, ""); !errors.Is(err, taskservice.ErrCommentLength) {
		t.Fatalf("AddComment error = %v, want ErrCommentLength", err)
	}
}
