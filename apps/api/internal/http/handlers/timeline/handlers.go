package timeline

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// ListForTask handles GET /tasks/{id}/timeline. The route must be mounted
// behind RequireTaskAccess so the workspace and task contexts are populated.
func ListForTask(deps Deps) func(context.Context, *ListForTaskInput) (*ListOutput, error) {
	return func(ctx context.Context, in *ListForTaskInput) (*ListOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListEventsForTask(ctx, generated.ListEventsForTaskParams{
			WorkspaceID:  ws.ID,
			TaskPublicID: sql.NullString{String: task.PublicID.String(), Valid: true},
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListOutput{}
		out.Body.Events = []Event{}
		for _, r := range rows {
			out.Body.Events = append(out.Body.Events, Event{
				ID:               r.PublicID.String(),
				TaskID:           nullStr(r.TaskPublicID),
				ActorUserID:      nullStr(r.ActorUserPublicID),
				ActorDisplayName: nullStr(r.ActorDisplayName),
				Type:             r.Type,
				Payload:          r.PayloadJson,
				OccurredAt:       r.OccurredAt,
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// ListForWorkspace handles GET /workspaces/{wsId}/timeline. The route must
// be mounted behind RequireWorkspaceMember.
func ListForWorkspace(deps Deps) func(context.Context, *ListForWorkspaceInput) (*ListOutput, error) {
	return func(ctx context.Context, in *ListForWorkspaceInput) (*ListOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListEventsForWorkspace(ctx, generated.ListEventsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListOutput{}
		out.Body.Events = []Event{}
		for _, r := range rows {
			out.Body.Events = append(out.Body.Events, Event{
				ID:               r.PublicID.String(),
				TaskID:           nullStr(r.TaskPublicID),
				ActorUserID:      nullStr(r.ActorUserPublicID),
				ActorDisplayName: nullStr(r.ActorDisplayName),
				Type:             r.Type,
				Payload:          r.PayloadJson,
				OccurredAt:       r.OccurredAt,
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
