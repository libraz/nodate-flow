package projects

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Create handles POST /workspaces/{wsId}/projects.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		slug := strings.ToLower(strings.TrimSpace(in.Body.Slug))
		if slug == "" {
			return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
		}

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		color := sql.NullString{String: in.Body.Color, Valid: in.Body.Color != ""}
		if _, err := deps.Queries.CreateProject(ctx, generated.CreateProjectParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			Slug:        slug,
			Name:        in.Body.Name,
			Description: desc,
			Color:       color,
		}); err != nil {
			// MySQL duplicate key check would map to slug-taken; for the
			// scaffold we collapse all errors to the unexpected bucket
			// except where the surface is obviously wrong.
			return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
		}

		return &CreateOutput{Body: Project{
			ID:          pub.String(),
			Slug:        slug,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			Color:       in.Body.Color,
			CreatedAt:   time.Now(),
		}}, nil
	}
}
