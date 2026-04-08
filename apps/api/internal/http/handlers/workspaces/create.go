package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Create handles POST /workspaces. The authenticated actor becomes the
// owner of the new workspace.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		slug := strings.ToLower(strings.TrimSpace(in.Body.Slug))
		if slug == "" {
			return nil, httpErr(apierrors.WsWorkspaceSlugAlreadyTaken)
		}
		// Conflict check.
		if _, err := deps.Queries.FindWorkspaceBySlug(ctx, slug); err == nil {
			return nil, httpErr(apierrors.WsWorkspaceSlugAlreadyTaken)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		icon := sql.NullString{String: in.Body.IconURL, Valid: in.Body.IconURL != ""}
		wsID, err := deps.Queries.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
			PublicID:    pub,
			Slug:        slug,
			Name:        in.Body.Name,
			Description: desc,
			IconUrl:     icon,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := sql.NullTime{Time: time.Now(), Valid: true}
		memPub := types.New()
		if _, err := deps.Queries.CreateWorkspaceMember(ctx, generated.CreateWorkspaceMemberParams{
			PublicID:    memPub,
			WorkspaceID: uint32(wsID),
			UserID:      uid,
			Role:        generated.WorkspaceMembersRoleOwner,
			JoinedAt:    now,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &CreateOutput{Body: Workspace{
			ID:          pub.String(),
			Slug:        slug,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			IconURL:     in.Body.IconURL,
			Role:        string(generated.WorkspaceMembersRoleOwner),
			CreatedAt:   time.Now(),
		}}
		return out, nil
	}
}
