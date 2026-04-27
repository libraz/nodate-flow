package workspace

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Disable handles DELETE /workspaces/{wsId}, soft-disabling the workspace.
// Only workspace owners may call this; enforced via RequireWorkspaceRole.
func Disable(deps Deps) func(context.Context, *DisableWorkspaceInput) (*DisableWorkspaceOutput, error) {
	return func(ctx context.Context, _ *DisableWorkspaceInput) (*DisableWorkspaceOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if err := deps.Queries.DisableWorkspace(ctx, types.FromUUID(ws.PublicID)); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if actorID, ok := authn.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "workspace.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "workspace",
				ResourceID:   ws.PublicID.String(),
			})
		}

		out := &DisableWorkspaceOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
