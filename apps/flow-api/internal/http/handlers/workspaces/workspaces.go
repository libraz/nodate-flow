package workspaces

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Deps holds the dependencies required by workspace handlers.
type Deps struct {
	Queries *generated.Queries
}

// WorkspaceResponse is the JSON representation of a workspace.
// The shape mirrors auth-api's Workspace DTO so a single TS type covers
// callers of either service.
type WorkspaceResponse struct {
	ID          string `json:"id" doc:"Workspace public id (UUID v7)"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	Timezone    string `json:"timezone"`
	Country     string `json:"country"`
	Role        string `json:"role,omitempty" doc:"Caller's role in this workspace"`
	MemberCount int64  `json:"memberCount" doc:"Number of enabled members in this workspace"`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// ListInput is the input for the list workspaces endpoint.
type ListInput struct{}

// WorkspacesListOutputBody is the response body for GET /workspaces.
type WorkspacesListOutputBody struct {
	Total      int64               `json:"total"`
	Workspaces []WorkspaceResponse `json:"workspaces"`
	NextCursor *string             `json:"nextCursor"`
}

// ListOutput is the response for the list workspaces endpoint.
type ListOutput struct {
	Body WorkspacesListOutputBody
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
		out.Body.Workspaces = make([]WorkspaceResponse, len(rows))
		for i, r := range rows {
			out.Body.Workspaces[i] = WorkspaceResponse{
				ID:          r.PublicID.String(),
				Slug:        r.Slug,
				Name:        r.Name,
				Description: handlerutil.NullStr(r.Description),
				IconURL:     handlerutil.NullStr(r.IconUrl),
				Timezone:    r.Timezone,
				Country:     handlerutil.NullStr(r.Country),
				Role:        string(r.Role),
				MemberCount: r.MemberCount,
				UpdatedAt:   handlerutil.NullTimeUnix(r.UpdatedAt),
				CreatedAt:   r.CreatedAt.Unix(),
			}
		}
		if len(rows) > 0 {
			out.Body.Total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		out.Body.NextCursor = nil
		return out, nil
	}
}
