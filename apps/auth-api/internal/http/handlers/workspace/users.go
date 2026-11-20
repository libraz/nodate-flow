package workspace

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

// ListUsers handles GET /workspaces/{wsId}/users. It returns a minimal
// user summary for every active member of the workspace, intended for
// actor-filter pickers (timeline, assignee, etc.).
func ListUsers(deps Deps) func(context.Context, *ListWorkspaceUsersInput) (*ListWorkspaceUsersOutput, error) {
	return func(ctx context.Context, _ *ListWorkspaceUsersInput) (*ListWorkspaceUsersOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListWorkspaceMembers(ctx, generated.ListWorkspaceMembersParams{
			WorkspaceID: ws.ID,
			Limit:       1000,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListWorkspaceUsersOutput{}
		out.Body.Users = make([]WorkspaceUserSummary, 0, len(rows))
		for _, r := range rows {
			out.Body.Users = append(out.Body.Users, WorkspaceUserSummary{
				ID:          r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				AvatarURL:   avatarutil.Resolve(r.AvatarStorageObjectID, r.AvatarUrl, r.UserPublicID.String(), ""),
			})
		}
		return out, nil
	}
}
