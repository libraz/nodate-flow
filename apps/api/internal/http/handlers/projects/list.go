package projects

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// List handles GET /workspaces/{wsId}/projects.
//
// ACL: workspace owners and admins see every enabled project in the
// workspace. Every other workspace role (including guest / member) only
// sees projects they are an enabled project_members row of. This matches
// the per-task visibility rules already enforced by the timeline ACL
// predicate and /projects/{prjId}/* routes, so listing never enumerates
// projects the caller cannot open.
func List(deps Deps) func(context.Context, *ListProjectsInput) (*ListProjectsOutput, error) {
	return func(ctx context.Context, in *ListProjectsInput) (*ListProjectsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListProjectsForWorkspace(ctx, generated.ListProjectsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Build the visibility set for non-privileged callers.
		var allowed map[string]struct{}
		isPrivileged := ws.Role == middleware.WorkspaceRoleOwner || ws.Role == middleware.WorkspaceRoleAdmin
		if !isPrivileged {
			userID, hasUser := middleware.ActorFromContext(ctx)
			if !hasUser {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			memberIDs, mErr := deps.Queries.ListProjectPublicIdsForUserInWorkspace(ctx, generated.ListProjectPublicIdsForUserInWorkspaceParams{
				WorkspaceID: ws.ID,
				UserID:      userID,
			})
			if mErr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			allowed = make(map[string]struct{}, len(memberIDs))
			for _, id := range memberIDs {
				allowed[id.String()] = struct{}{}
			}
		}

		out := &ListProjectsOutput{}
		out.Body.Projects = make([]Project, 0, len(rows))
		wsPublicID := ws.PublicID.String()
		for _, r := range rows {
			if !isPrivileged {
				if _, ok := allowed[r.PublicID.String()]; !ok {
					continue
				}
			}
			out.Body.Projects = append(out.Body.Projects, rowToProjectFromList(r, wsPublicID))
		}
		// Note: Total is the unfiltered window count from v_projects.
		// For privileged callers it matches the returned list; for
		// filtered callers it may overstate. Left as-is rather than
		// recounting — the filtered list is the authoritative length.
		out.Body.Total = int64(len(out.Body.Projects))
		return out, nil
	}
}
