package projects

import (
	"context"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// Disable handles DELETE /projects/{prjId}.
//
// The handler runs DisableProjectChildTasks and DisableProject in a single
// transaction so the cascade is atomic — callers reading the underlying
// tables (raw SELECTs, MCP tooling, replay jobs) never observe a window
// where the project is disabled but its tasks still claim enabled = TRUE,
// nor the inverse. The view layer (v_task_list, v_task_detail) already
// AND-filters projects.enabled, but bypassing the view would otherwise
// leak orphan-looking rows.
func Disable(deps Deps) func(context.Context, *DisableProjectInput) (*DisableProjectOutput, error) {
	return func(ctx context.Context, _ *DisableProjectInput) (*DisableProjectOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}

		// Snapshot the count of currently-enabled child tasks BEFORE the
		// cascade fires so the audit entry reflects the size of the
		// affected set. Doing this inside the same transaction (after the
		// implicit row-set lock added by the UPDATE) would also work but
		// pre-counting is the simplest path that does not depend on the
		// :exec-only DisableProjectChildTasks generated method exposing a
		// sql.Result handle.
		var childCount int64
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks
			 WHERE workspace_id = ? AND project_id = ? AND enabled = TRUE`,
			ws.ID, prj.ID,
		).Scan(&childCount); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: count child tasks failed",
				slog.Any("err", err),
				slog.String("handler", "projects.Disable"),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("project", prj.PublicID),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)

		if err := qtx.DisableProjectChildTasks(ctx, generated.DisableProjectChildTasksParams{
			WorkspaceID: ws.ID,
			ProjectID:   prj.ID,
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: child task cascade failed",
				slog.Any("err", err),
				slog.String("handler", "projects.Disable"),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("project", prj.PublicID),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := qtx.DisableProject(ctx, generated.DisableProjectParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(prj.PublicID),
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: project disable failed",
				slog.Any("err", err),
				slog.String("handler", "projects.Disable"),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("project", prj.PublicID),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		nflog.LoggerFromContext(ctx).InfoContext(ctx, "project disabled with cascade",
			logutil.LogEntity("workspace", ws.PublicID),
			logutil.LogEntity("project", prj.PublicID),
			slog.Int64("child_tasks_disabled", childCount),
		)

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "project.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "project",
				ResourceID:   prj.PublicID.String(),
				Metadata: map[string]any{
					"projectPublicId":    prj.PublicID.String(),
					"childTasksDisabled": childCount,
				},
			})
		}

		out := &DisableProjectOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
