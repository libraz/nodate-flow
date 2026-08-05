package tasks

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}

		if spec := requireProjectEditor(ctx, deps.DB, prj.WorkspaceID, prj.ID, actorID); spec != nil {
			return nil, httpErr(spec)
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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
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

		qtx := deps.Queries.WithTx(tx)
		actorAuditID := sql.NullInt32{Int32: int32(actorID), Valid: actorID != 0} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		for _, item := range items {
			if err := qtx.UpdateTaskSortWeight(ctx, generated.UpdateTaskSortWeightParams{
				SortWeight:      item.sortWeight,
				UpdatedByUserID: actorAuditID,
				ID:              item.internalID,
				WorkspaceID:     prj.WorkspaceID,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.reorder",
			ActorID:      actorID,
			WorkspaceID:  prj.WorkspaceID,
			ResourceType: "project",
			ResourceID:   prjPub.String(),
		})

		out := &ReorderTasksOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
