package tasks

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListMyTasks handles GET /me/tasks. It returns every task where the
// authenticated user is attached as an actor across every workspace
// they belong to, backed by the ListMyTasksGlobal sqlc query which
// joins v_my_tasks with workspaces so callers get workspace context
// per row without a second round-trip. Used by the web client's
// cross-workspace "Today" and Calendar views.
func ListMyTasks(deps Deps) func(context.Context, *ListMyTasksInput) (*ListMyTasksOutput, error) {
	return func(ctx context.Context, in *ListMyTasksInput) (*ListMyTasksOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		profile, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}

		rows, err := deps.Queries.ListMyTasksGlobal(ctx, generated.ListMyTasksGlobalParams{
			UserPublicID: profile.PublicID,
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListMyTasksOutput{}
		out.Body.Tasks = []MyTaskListItem{}
		for _, r := range rows {
			out.Body.Tasks = append(out.Body.Tasks, rowToMyTaskListItem(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

func rowToMyTaskListItem(r generated.ListMyTasksGlobalRow) MyTaskListItem {
	return MyTaskListItem{
		ID:            r.PublicID.String(),
		WorkspaceID:   r.WorkspacePublicID.String(),
		WorkspaceName: r.WorkspaceName,
		ProjectID:     bytesToUUIDString(r.ProjectPublicID),
		ProjectName:   r.ProjectName,
		Title:         r.Title,
		DerivedState:  string(r.DerivedState),
		Priority:      r.Priority,
		DueOn:         nullDate(r.DueOn),
		EventOn:       nullDate(r.EventOn),
		ActorRole:     string(r.ActorRole),
		UpdatedAt:     nullTime(r.UpdatedAt),
		CreatedAt:     r.CreatedAt,
	}
}
