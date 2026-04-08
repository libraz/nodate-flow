package workspaces

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Disable handles DELETE /workspaces/{wsId}, soft-disabling the workspace.
// Only workspace owners may call this; enforced via RequireWorkspaceRole.
func Disable(deps Deps) func(context.Context, *DisableInput) (*DisableOutput, error) {
	return func(ctx context.Context, in *DisableInput) (*DisableOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if err := deps.Queries.DisableWorkspace(ctx, types.FromUUID(ws.PublicID)); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &DisableOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
