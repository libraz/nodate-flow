package mcp_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mcpProjectRoleFixture is a tenant whose members sit at every rung of the
// project-role ladder around one PUBLIC task. Public visibility is the point:
// each of these users passes the Layer-4 read gate, so a denial can only come
// from the Layer-3 project-role floor.
type mcpProjectRoleFixture struct {
	wsID       uint32
	projectPub uuid.UUID
	taskPub    uuid.UUID

	editorID    uint32
	commenterID uint32
	viewerID    uint32
	// outsiderID is a workspace member with no project_members row at all:
	// the caller REST refuses and MCP must refuse identically.
	outsiderID uint32
}

func seedMCPProjectRoleFixture(t *testing.T, db *sql.DB) *mcpProjectRoleFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	suffix := uuid.New().String()[:8]

	insertUser := func(role string) uint32 {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale)
			 VALUES (?, ?, ?, 'en')`,
			pub[:], "mcprole-"+role+"-"+suffix+"@example.test", "MCPRole "+role)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	editor := insertUser("editor")
	commenter := insertUser("commenter")
	viewer := insertUser("viewer")
	outsider := insertUser("outsider")

	wsPub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "mcprole-ws-"+suffix, "MCPRole Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Everyone is a plain workspace member: nobody gets the owner/admin
	// elevation bypass, so the project role is the only thing under test.
	for _, userID := range []uint32{editor, commenter, viewer, outsider} {
		mpub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
			 VALUES (?, ?, ?, 'member')`,
			mpub[:], wsID, userID)
		require.NoError(t, err)
	}

	prjPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		prjPub[:], wsID, "mcprole-prj-"+suffix, "MCPRole Project", "MR"+suffix[:3])
	require.NoError(t, err)
	prjID64, err := res.LastInsertId()
	require.NoError(t, err)
	prjID := uint32(prjID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	for userID, role := range map[uint32]string{
		editor:    "editor",
		commenter: "commenter",
		viewer:    "viewer",
	} {
		pmpub := uuid.Must(uuid.NewV7())
		_, err = tx.ExecContext(ctx,
			`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role)
			 VALUES (?, ?, ?, ?, ?)`,
			pmpub[:], wsID, prjID, userID, role)
		require.NoError(t, err)
	}

	const taskNumber = 11
	taskPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, ?, ?, 'public', ?)`,
		taskPub[:], wsID, prjID, taskNumber, "Shared task", editor)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
	committed = true

	return &mcpProjectRoleFixture{
		wsID:        wsID,
		projectPub:  prjPub,
		taskPub:     taskPub,
		editorID:    editor,
		commenterID: commenter,
		viewerID:    viewer,
		outsiderID:  outsider,
	}
}

func requireSpec(t *testing.T, err error, want *apierrors.Spec) {
	t.Helper()
	require.Error(t, err)
	var ae *apierrors.APIError
	require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	require.Equalf(t, want.Code, ae.Spec.Code, "want %s, got %s", want.Code, ae.Spec.Code)
}

// TestMCPWriteToolsEnforceProjectRole proves the MCP write tools apply the
// same Layer-3 project-role floor as their REST counterparts: a workspace
// member holding a write:workspace MCP token still cannot mutate a task in a
// project they are not an editor of, and the denial carries the same
// WS.PROJECT.ACCESS_DENIED code REST returns.
//
// Every actor here can read the task (it is PUBLIC), which is what separates
// this from the Layer-4 visibility regression: the reads succeed and only the
// writes are refused.
func TestMCPWriteToolsEnforceProjectRole(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPProjectRoleFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()

	scopes := []string{"write:workspace"}
	outsider := mcp.NewTestSession(fx.outsiderID, fx.wsID, scopes)
	viewer := mcp.NewTestSession(fx.viewerID, fx.wsID, scopes)
	commenter := mcp.NewTestSession(fx.commenterID, fx.wsID, scopes)
	editor := mcp.NewTestSession(fx.editorID, fx.wsID, scopes)

	taskArg := mcpVisJSON(t, map[string]any{"taskId": fx.taskPub.String()})

	t.Run("outsider/can_read_the_task", func(t *testing.T) {
		// Positive control: the denials below are the project-role floor, not
		// a task the caller was never able to see in the first place.
		_, err := mcp.RunGetTask(ctx, deps, outsider, taskArg)
		require.NoError(t, err)
	})

	t.Run("update_task/denied_without_project_membership", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"title":  "hijacked",
		})
		_, err := mcp.RunUpdateTask(ctx, deps, outsider, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("update_task/denied_for_viewer", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"title":  "hijacked",
		})
		_, err := mcp.RunUpdateTask(ctx, deps, viewer, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("update_task/denied_for_commenter", func(t *testing.T) {
		// Structural edits sit above the conversational floor, matching the
		// REST split between the commenter and editor route groups.
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"title":  "hijacked",
		})
		_, err := mcp.RunUpdateTask(ctx, deps, commenter, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("transition_task/denied_without_project_membership", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId":     fx.taskPub.String(),
			"transition": "start",
		})
		_, err := mcp.RunTransitionTask(ctx, deps, outsider, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("add_task_label/denied_without_project_membership", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId":  fx.taskPub.String(),
			"labelId": uuid.Must(uuid.NewV7()).String(),
		})
		_, err := mcp.RunAddTaskLabel(ctx, deps, outsider, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("archive_task/denied_without_project_membership", func(t *testing.T) {
		_, err := mcp.RunArchiveTask(ctx, deps, outsider, taskArg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("add_comment/denied_without_project_membership", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"body":   "from an outsider",
		})
		_, err := mcp.RunAddComment(ctx, deps, outsider, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("add_comment/denied_for_viewer", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"body":   "from a viewer",
		})
		_, err := mcp.RunAddComment(ctx, deps, viewer, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})

	t.Run("add_comment/allowed_for_commenter", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"body":   "from a commenter",
		})
		_, err := mcp.RunAddComment(ctx, deps, commenter, arg)
		require.NoError(t, err)
	})

	t.Run("update_task/allowed_for_editor", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"taskId": fx.taskPub.String(),
			"title":  "edited by the project editor",
		})
		_, err := mcp.RunUpdateTask(ctx, deps, editor, arg)
		require.NoError(t, err)
	})

	t.Run("create_task/denied_without_project_membership", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"projectId": fx.projectPub.String(),
			"title":     "smuggled in",
		})
		_, err := mcp.RunCreateTask(ctx, deps, outsider, arg)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})
}
