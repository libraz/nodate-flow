package workspaces

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
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

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
// MySQL drivers may return int64 or []byte depending on column type.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}
