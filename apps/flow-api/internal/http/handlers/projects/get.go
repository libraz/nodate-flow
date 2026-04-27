package projects

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// Get handles GET /projects/{prjId}. The project context has already been
// resolved by RequireProjectMemberByGlobalID.
func Get(deps Deps) func(context.Context, *GetProjectInput) (*GetProjectOutput, error) {
	return func(ctx context.Context, _ *GetProjectInput) (*GetProjectOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		row, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, types.FromUUID(prj.PublicID))
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}
		return &GetProjectOutput{Body: rowToProjectFromFind(row)}, nil
	}
}
