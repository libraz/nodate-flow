package projects

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// ListDependencies handles GET /projects/{prjId}/dependencies. It returns
// every task_dependencies edge where both endpoints belong to the given
// project. The Gantt view consumes this to draw dependency arrows; the
// List / Board views use it to compute "blocked by open" badges.
func ListDependencies(deps Deps) func(context.Context, *ListProjectDependenciesInput) (*ListProjectDependenciesOutput, error) {
	return func(ctx context.Context, _ *ListProjectDependenciesInput) (*ListProjectDependenciesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		rows, err := deps.Queries.ListDependenciesForProject(ctx, generated.ListDependenciesForProjectParams{
			WorkspaceID:     ws.ID,
			ProjectPublicID: types.FromUUID(prj.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListProjectDependenciesOutput{}
		out.Body.Edges = make([]ProjectDependencyEdge, 0, len(rows))
		for _, r := range rows {
			out.Body.Edges = append(out.Body.Edges, ProjectDependencyEdge{
				ID:                   r.PublicID.String(),
				Kind:                 string(r.Kind),
				FromTaskID:           r.FromTaskPublicID.String(),
				FromTaskDerivedState: string(r.FromTaskDerivedState),
				ToTaskID:             r.ToTaskPublicID.String(),
				ToTaskDerivedState:   string(r.ToTaskDerivedState),
			})
		}
		return out, nil
	}
}
