package projects

import (
	"context"
	"errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// Disable handles DELETE /projects/{prjId}.
//
// One transaction locks the project's enabled tasks, deletes each through
// itemkit (the same path as a single task delete, so each emits its
// item.deleted / task.disabled events and cascades linked calendar events)
// and then disables the project. Readers of the underlying tables
// never see a disabled project with live tasks, nor the inverse.
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
		actorID, _ := middleware.ActorFromContext(ctx)

		var childCount int64
		var notFound bool
		txErr := dbretry.InTx(ctx, deps.DB, "projects.Disable", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			childCount, notFound = 0, false
			qtx := deps.Queries.WithTx(tx.RawTx())
			ids, err := qtx.ListEnabledProjectTaskIDsForUpdate(ctx, generated.ListEnabledProjectTaskIDsForUpdateParams{
				WorkspaceID: ws.ID,
				ProjectID:   prj.ID,
			})
			if err != nil {
				nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: child task list failed",
					slog.Any("err", err),
					slog.String("handler", "projects.Disable"),
					logutil.LogEntity("workspace", ws.PublicID),
					logutil.LogEntity("project", prj.PublicID),
				)
				return err
			}
			for _, id := range ids {
				if _, err := itemkit.DeleteTask(ctx, tx, ws.ID, id, actorID); err != nil {
					nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: child task cascade failed",
						slog.Any("err", err),
						slog.String("handler", "projects.Disable"),
						logutil.LogEntity("workspace", ws.PublicID),
						logutil.LogEntity("project", prj.PublicID),
					)
					return err
				}
			}
			childCount = int64(len(ids))

			// Only matches a project that is still live, so a zero count
			// means the caller named a project that is not there to archive.
			rows, err := qtx.DisableProject(ctx, generated.DisableProjectParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(prj.PublicID),
			})
			if err != nil {
				nflog.LoggerFromContext(ctx).ErrorContext(ctx, "projects.Disable: project disable failed",
					slog.Any("err", err),
					slog.String("handler", "projects.Disable"),
					logutil.LogEntity("workspace", ws.PublicID),
					logutil.LogEntity("project", prj.PublicID),
				)
				return err
			}
			if rows == 0 {
				notFound = true
				return errors.New("project not found")
			}
			return nil
		})
		if notFound {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		nflog.LoggerFromContext(ctx).InfoContext(ctx, "project disabled with cascade",
			logutil.LogEntity("workspace", ws.PublicID),
			logutil.LogEntity("project", prj.PublicID),
			logutil.LogNumber("child_tasks_disabled", childCount),
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
