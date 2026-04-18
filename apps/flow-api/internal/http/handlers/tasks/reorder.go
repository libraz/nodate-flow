package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Reorder handles POST /tasks/reorder. It accepts a batch of task
// public IDs with new sort_weight values and updates them all inside a
// single transaction. All tasks must belong to the given project, which
// itself must belong to a workspace the actor has access to.
func Reorder(deps Deps) func(context.Context, *ReorderTasksInput) (*ReorderTasksOutput, error) {
	return func(ctx context.Context, in *ReorderTasksInput) (*ReorderTasksOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}

		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Workspace membership check.
		const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
		var one int
		if err := deps.DB.QueryRowContext(ctx, wsMemQuery, prj.WorkspaceID, actorID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectAccessDenied)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Resolve all public IDs to internal IDs and verify project membership.
		type resolved struct {
			internalID uint32
			sortWeight int32
		}
		items := make([]resolved, 0, len(in.Body.Items))
		const taskLookup = `SELECT id, project_id FROM tasks
WHERE public_id = ? AND workspace_id = ? AND enabled = TRUE LIMIT 1`

		for _, item := range in.Body.Items {
			pub, err := types.Parse(item.ID)
			if err != nil {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			var taskID uint32
			var taskProjectID uint32
			if err := deps.DB.QueryRowContext(ctx, taskLookup, pub, prj.WorkspaceID).Scan(&taskID, &taskProjectID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsTaskNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if taskProjectID != prj.ID {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			items = append(items, resolved{internalID: taskID, sortWeight: item.SortWeight})
		}

		// Update all sort weights inside a single transaction.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		const updateSQL = `UPDATE tasks SET sort_weight = ? WHERE id = ? AND workspace_id = ? AND enabled = TRUE`
		for _, item := range items {
			if _, err := tx.ExecContext(ctx, updateSQL, item.sortWeight, item.internalID, prj.WorkspaceID); err != nil {
				return nil, fmt.Errorf("reorder: update task %d: %w", item.internalID, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ReorderTasksOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
