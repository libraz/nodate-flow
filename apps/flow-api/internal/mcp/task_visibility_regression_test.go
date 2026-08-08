package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// mcpVisibilityFixture is a minimal tenant used to prove MCP tools enforce
// Layer-4 task visibility: a PRIVATE task created by one workspace member is
// invisible to another (non-admin) member who is neither the creator nor a
// task actor.
type mcpVisibilityFixture struct {
	wsID          uint32
	creatorID     uint32
	otherMemberID uint32
	privateTask   uuid.UUID
	projectKey    string
}

func seedMCPVisibilityFixture(t *testing.T, db *sql.DB) *mcpVisibilityFixture {
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
			pub[:], "mcpvis-"+role+"-"+suffix+"@example.test", "MCPVis "+role)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	creator := insertUser("creator")
	other := insertUser("other")

	pub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		pub[:], "mcpvis-ws-"+suffix, "MCPVis Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	insertMember := func(userID uint32) {
		mpub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
			 VALUES (?, ?, ?, 'member')`,
			mpub[:], wsID, userID)
		require.NoError(t, err)
	}
	insertMember(creator)
	insertMember(other)

	projectKey := "MV" + suffix[:3]
	ppub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		ppub[:], wsID, "mcpvis-prj-"+suffix, "MCPVis Project", projectKey)
	require.NoError(t, err)
	prjID64, err := res.LastInsertId()
	require.NoError(t, err)
	prjID := uint32(prjID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// The creator is a project member; the other member is not, so the
	// PRIVATE task is only reachable by the creator via the visibility path.
	pmpub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role)
		 VALUES (?, ?, ?, ?, 'editor')`,
		pmpub[:], wsID, prjID, creator)
	require.NoError(t, err)

	const taskNumber = 7
	taskPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, ?, ?, 'private', ?)`,
		taskPub[:], wsID, prjID, taskNumber, "Secret task", creator)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
	committed = true

	return &mcpVisibilityFixture{
		wsID:          wsID,
		creatorID:     creator,
		otherMemberID: other,
		privateTask:   taskPub,
		projectKey:    projectKey,
	}
}

func mcpVisJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// apiErrorCode extracts the error code an MCP tool answered with, so two
// rejections can be compared for indistinguishability rather than merely
// for both being errors.
func apiErrorCode(t *testing.T, err error) string {
	t.Helper()
	var ae *apierrors.APIError
	require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	return ae.Spec.Code
}

// mcpVisJSONString renders a tool result back to JSON so a test can assert
// on what the caller would actually receive.
func mcpVisJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func requireTaskNotFound(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var ae *apierrors.APIError
	require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	require.Equalf(t, apierrors.WsTaskNotFound.Code, ae.Spec.Code,
		"want %s, got %s", apierrors.WsTaskNotFound.Code, ae.Spec.Code)
}

// TestMCPToolsEnforceTaskVisibility proves that a workspace member who cannot
// see another member's PRIVATE task gets WS.TASK.NOT_FOUND from every tool
// that reads or mutates a task by id, generate_page rejects (not skips) the
// invisible task, and resolve_task_ref cannot be used as an existence oracle
// (a missing ref and an existing-but-invisible one are indistinguishable).
func TestMCPToolsEnforceTaskVisibility(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPVisibilityFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()

	other := mcp.NewTestSession(fx.otherMemberID, fx.wsID, []string{"write:workspace"})
	creator := mcp.NewTestSession(fx.creatorID, fx.wsID, []string{"write:workspace"})

	taskArg := mcpVisJSON(t, map[string]any{"taskId": fx.privateTask.String()})

	t.Run("archive_task/denied", func(t *testing.T) {
		_, err := mcp.RunArchiveTask(ctx, deps, other, taskArg)
		requireTaskNotFound(t, err)
	})
	t.Run("unarchive_task/denied", func(t *testing.T) {
		_, err := mcp.RunUnarchiveTask(ctx, deps, other, taskArg)
		requireTaskNotFound(t, err)
	})
	t.Run("propose_priority/denied", func(t *testing.T) {
		_, err := mcp.RunProposePriority(ctx, deps, other, taskArg)
		requireTaskNotFound(t, err)
	})
	t.Run("propose_steps/denied", func(t *testing.T) {
		_, err := mcp.RunProposeSteps(ctx, deps, other, taskArg)
		requireTaskNotFound(t, err)
	})

	t.Run("generate_page/rejects_invisible_task", func(t *testing.T) {
		arg := mcpVisJSON(t, map[string]any{
			"contextDescription": "summary",
			"taskIds":            []string{fx.privateTask.String()},
		})
		_, err := mcp.RunGeneratePage(ctx, deps, other, arg)
		requireTaskNotFound(t, err)
	})

	t.Run("add_favorite/not_visible_matches_missing", func(t *testing.T) {
		// A favorite grants nothing and nobody else can see it, which is
		// exactly why the existence check was left at workspace scope. The
		// leak is not the row: it is that accepting the call tells the
		// caller the id names a real task. Favoriting an id that is present
		// but invisible must answer the same as favoriting an id that was
		// never issued.
		invisible := mcpVisJSON(t, map[string]any{
			"targetType": "task",
			"targetId":   fx.privateTask.String(),
		})
		absent := mcpVisJSON(t, map[string]any{
			"targetType": "task",
			"targetId":   uuid.Must(uuid.NewV7()).String(),
		})

		_, invisibleErr := mcp.RunAddFavorite(ctx, deps, other, invisible)
		require.Error(t, invisibleErr)
		_, absentErr := mcp.RunAddFavorite(ctx, deps, other, absent)
		require.Error(t, absentErr)
		require.Equalf(t, apiErrorCode(t, absentErr), apiErrorCode(t, invisibleErr),
			"an invisible task and an absent one must be indistinguishable; got %v vs %v",
			invisibleErr, absentErr)

		// And nothing was written: a favorite row for a task the caller
		// cannot see would resurface its id in list_favorites.
		listed, err := mcp.RunListFavorites(ctx, deps, other, mcpVisJSON(t, map[string]any{}))
		require.NoError(t, err)
		require.NotContains(t, mcpVisJSONString(t, listed), fx.privateTask.String())

		// Positive control: the creator, who can see the task, may favorite it.
		_, err = mcp.RunAddFavorite(ctx, deps, creator, invisible)
		require.NoError(t, err)
	})

	t.Run("resolve_task_ref/not_visible_matches_missing", func(t *testing.T) {
		visibleRef := mcpVisJSON(t, map[string]any{"ref": fx.projectKey + "-7"})
		missingRef := mcpVisJSON(t, map[string]any{"ref": fx.projectKey + "-9999"})

		// The non-member gets the same NOT_FOUND for an existing-but-private
		// task and a genuinely missing one: no existence oracle.
		_, notVisibleErr := mcp.RunResolveTaskRef(ctx, deps, other, visibleRef)
		requireTaskNotFound(t, notVisibleErr)
		_, missingErr := mcp.RunResolveTaskRef(ctx, deps, other, missingRef)
		requireTaskNotFound(t, missingErr)

		// Positive control: the creator, who can see the task, resolves it.
		out, err := mcp.RunResolveTaskRef(ctx, deps, creator, visibleRef)
		require.NoError(t, err)
		m, ok := out.(map[string]any)
		require.True(t, ok)
		require.Equal(t, fx.privateTask.String(), m["taskId"])
	})
}

// requireMCPIntegration gates the DB-backed MCP tests. Once integration
// mode is on the test boots the shared MySQL testcontainer used
// elsewhere in the suite; if Docker is unreachable, helpers.StartShared
// fails the test rather than skipping it.
func requireMCPIntegration(t *testing.T) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
}
