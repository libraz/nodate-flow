package workspaces

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// List handles GET /workspaces and returns the workspaces the actor belongs
// to.
func List(deps Deps) func(context.Context, *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
	return func(ctx context.Context, in *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListWorkspacesForUser(ctx, generated.ListWorkspacesForUserParams{
			UserID: uid,
			Limit:  limit,
			Offset: in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListWorkspacesOutput{}
		out.Body.Workspaces = make([]Workspace, 0, len(rows))
		for _, r := range rows {
			out.Body.Workspaces = append(out.Body.Workspaces, rowToWorkspaceFromList(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64
