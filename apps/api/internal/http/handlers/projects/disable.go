package projects

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Disable handles DELETE /projects/{prjId}.
func Disable(deps Deps) func(context.Context, *DisableInput) (*DisableOutput, error) {
	return func(ctx context.Context, in *DisableInput) (*DisableOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		if err := deps.Queries.DisableProject(ctx, generated.DisableProjectParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(prj.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &DisableOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
