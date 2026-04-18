package projects

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Get handles GET /projects/{prjId}. The project context has already been
// resolved by RequireProjectMemberByGlobalId.
func Get(deps Deps) func(context.Context, *GetProjectInput) (*GetProjectOutput, error) {
	return func(ctx context.Context, in *GetProjectInput) (*GetProjectOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		row, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, types.FromUUID(prj.PublicID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &GetProjectOutput{Body: rowToProjectFromFind(row)}, nil
	}
}
