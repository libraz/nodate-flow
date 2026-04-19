package workspaces

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// Deps holds the dependencies required by workspace handlers.
type Deps struct {
	Queries *generated.Queries
}

// WorkspaceResponse is the JSON representation of a workspace.
type WorkspaceResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ListInput is the input for the list workspaces endpoint.
type ListInput struct{}

// ListOutput is the response for the list workspaces endpoint.
type ListOutput struct {
	Body struct {
		Items []WorkspaceResponse `json:"items"`
	}
}

// List returns all workspaces the authenticated user belongs to.
func List(deps Deps) func(context.Context, *ListInput) (*ListOutput, error) {
	return func(ctx context.Context, _ *ListInput) (*ListOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, huma.Error403Forbidden("Access denied")
		}

		rows, err := deps.Queries.ListWorkspacesForUser(ctx, generated.ListWorkspacesForUserParams{
			UserID: actorID,
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list workspaces", err)
		}

		out := &ListOutput{}
		out.Body.Items = make([]WorkspaceResponse, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = WorkspaceResponse{
				ID:   r.PublicID.String(),
				Name: r.Name,
				Slug: r.Slug,
			}
		}
		return out, nil
	}
}

