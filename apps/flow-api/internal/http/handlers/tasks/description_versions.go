package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// mapDescriptionVersionRow converts a ListDescriptionVersionsRow to the DTO.
func mapDescriptionVersionRow(r generated.ListDescriptionVersionsRow) DescriptionVersion {
	return DescriptionVersion{
		ID:                r.PublicID.String(),
		VersionNumber:     int(r.VersionNumber),
		AuthorID:          handlerutil.PublicIDOrEmpty(r.AuthorPublicID),
		AuthorDisplayName: nullStr(r.AuthorDisplayName),
		AuthorAgentID:     handlerutil.PublicIDOrEmpty(r.AuthorAgentPublicID),
		AuthorAgentName:   r.AuthorAgentName,
		BodyLength:        int(r.BodyLength),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// mapDescriptionVersionFull converts a FindDescriptionVersionRow to the full DTO.
func mapDescriptionVersionFull(r generated.FindDescriptionVersionRow) DescriptionVersionFull {
	return DescriptionVersionFull{
		DescriptionVersion: DescriptionVersion{
			ID:                r.PublicID.String(),
			VersionNumber:     int(r.VersionNumber),
			AuthorID:          handlerutil.PublicIDOrEmpty(r.AuthorPublicID),
			AuthorDisplayName: nullStr(r.AuthorDisplayName),
			AuthorAgentID:     handlerutil.PublicIDOrEmpty(r.AuthorAgentPublicID),
			AuthorAgentName:   r.AuthorAgentName,
			BodyLength:        int(r.BodyLength),
			CreatedAt:         r.CreatedAt.Unix(),
		},
		Body: r.Body,
	}
}

// ListDescriptionVersions handles GET /tasks/{id}/description-history.
func ListDescriptionVersions(deps Deps) func(context.Context, *ListDescriptionVersionsInput) (*ListDescriptionVersionsOutput, error) {
	return func(ctx context.Context, _ *ListDescriptionVersionsInput) (*ListDescriptionVersionsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		rows, err := deps.Queries.ListDescriptionVersions(ctx, generated.ListDescriptionVersionsParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListDescriptionVersionsOutput{}
		out.Body.Versions = make([]DescriptionVersion, 0, len(rows))
		for _, r := range rows {
			out.Body.Versions = append(out.Body.Versions, mapDescriptionVersionRow(r))
		}
		return out, nil
	}
}

// GetDescriptionVersion handles GET /tasks/{id}/description-history/{versionId}.
func GetDescriptionVersion(deps Deps) func(context.Context, *GetDescriptionVersionInput) (*GetDescriptionVersionOutput, error) {
	return func(ctx context.Context, in *GetDescriptionVersionInput) (*GetDescriptionVersionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		versionPub, err := types.Parse(in.VersionID)
		if err != nil {
			return nil, httpErr(apierrors.WsDescriptionVersionNotFound)
		}

		row, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			PublicID:    versionPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsDescriptionVersionNotFound, apierrors.InternalUnexpected))
		}

		return &GetDescriptionVersionOutput{Body: mapDescriptionVersionFull(row)}, nil
	}
}

// RestoreDescriptionVersion handles POST /tasks/{id}/description-history/{versionId}/restore.
//
// calendar-precondition: chronology not-applicable — a restore patches the description only, so the reschedule that mirrors a due date onto an event is never taken
func RestoreDescriptionVersion(deps Deps) func(context.Context, *RestoreDescriptionVersionInput) (*RestoreDescriptionVersionOutput, error) {
	return func(ctx context.Context, in *RestoreDescriptionVersionInput) (*RestoreDescriptionVersionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		versionPub, err := types.Parse(in.VersionID)
		if err != nil {
			return nil, httpErr(apierrors.WsDescriptionVersionNotFound)
		}

		// Find the version to restore.
		version, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			PublicID:    versionPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsDescriptionVersionNotFound, apierrors.InternalUnexpected))
		}

		actorID, _ := middleware.ActorFromContext(ctx)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}

		// The restored body becomes the newest version rather than
		// rewinding the history to the one it came from; taskupdate writes
		// it, its mentions and description.version.restored on one commit.
		var result taskupdate.Result
		if err := dbretry.InTx(ctx, deps.DB, "tasks.RestoreDescriptionVersion", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			var aerr error
			result, aerr = taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID:  ws.ID,
				TaskID:       task.ID,
				PublicID:     types.FromUUID(task.PublicID),
				Patch:        taskupdate.Patch{Description: &version.Body},
				Actor:        actor,
				RestoredFrom: versionPub,
			})
			return aerr
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "description_version.restore",
			ResourceType: "task",
			ResourceID:   types.FromUUID(task.PublicID).String(),
			Payload:      result.Payload,
			CallSite:     "tasks.RestoreDescriptionVersion (taskupdate.Apply appended the event)",
		})

		// A restore replaces the description the task is stored under, so
		// the embedding follows it. The title is untouched by this write,
		// so the row's own title is what pairs with the restored body.
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, task.ID, result.NewTitle.String(), version.Body)

		out := &RestoreDescriptionVersionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
