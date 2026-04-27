package workspace

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// List handles GET /workspaces and returns the workspaces the actor belongs
// to.
func List(deps Deps) func(context.Context, *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
	return func(ctx context.Context, in *ListWorkspacesInput) (*ListWorkspacesOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
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
		out.Body.Items = make([]Workspace, 0, len(rows))
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, rowToWorkspaceFromList(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
