package workspaces

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Get handles GET /workspaces/{wsId}. Workspace context has already been
// resolved by RequireWorkspaceMember.
func Get(deps Deps) func(context.Context, *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
	return func(ctx context.Context, in *GetWorkspaceInput) (*GetWorkspaceOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		row, err := deps.Queries.FindWorkspaceByPublicId(ctx, types.FromUUID(ws.PublicID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		dto := rowToWorkspaceFromFind(row)
		dto.Role = string(ws.Role)
		return &GetWorkspaceOutput{Body: dto}, nil
	}
}
