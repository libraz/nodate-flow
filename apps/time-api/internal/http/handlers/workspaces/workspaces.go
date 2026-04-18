package workspaces

import (
	"context"
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// Deps holds the dependencies required by workspace handlers.
type Deps struct {
	Queries *generated.Queries
	DB      *sql.DB
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

// CreateInput is the input for the create workspace endpoint.
type CreateInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"255" doc:"Workspace name"`
		Slug string `json:"slug" minLength:"1" maxLength:"255" doc:"Workspace URL slug"`
	}
}

// CreateOutput is the response for the create workspace endpoint.
type CreateOutput struct {
	Body WorkspaceResponse
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

// Create creates a new workspace and adds the caller as the owner member.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, input *CreateInput) (*CreateOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, huma.Error403Forbidden("Access denied")
		}

		wsPublicID := types.New()

		wsID, err := deps.Queries.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
			PublicID:    wsPublicID,
			Slug:        input.Body.Slug,
			Name:        input.Body.Name,
			Description: sql.NullString{},
			IconUrl:     sql.NullString{},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to create workspace", err)
		}

		memberPublicID := types.New()
		now := time.Now().UTC()
		_, err = deps.Queries.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
			PublicID:        memberPublicID,
			WorkspaceID:     uint32(wsID),
			UserID:          actorID,
			Role:            generated.WorkspaceMembersRoleOwner,
			InvitedByUserID: sql.NullInt32{},
			InvitedAt:       sql.NullTime{},
			JoinedAt:        sql.NullTime{Time: now, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to add workspace member", err)
		}

		out := &CreateOutput{}
		out.Body = WorkspaceResponse{
			ID:   wsPublicID.String(),
			Name: input.Body.Name,
			Slug: input.Body.Slug,
		}
		return out, nil
	}
}
