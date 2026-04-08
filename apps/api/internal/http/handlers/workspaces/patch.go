package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Patch handles PATCH /workspaces/{wsId}. Only workspace admins or owners
// may rename or re-slug a workspace; the role check is enforced at the
// router layer via RequireWorkspaceRole.
func Patch(deps Deps) func(context.Context, *PatchInput) (*PatchOutput, error) {
	return func(ctx context.Context, in *PatchInput) (*PatchOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		current, err := deps.Queries.FindWorkspaceByPublicId(ctx, types.FromUUID(ws.PublicID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsWorkspaceNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		newName := current.Name
		if in.Body.Name != nil && *in.Body.Name != "" {
			newName = *in.Body.Name
		}
		newSlug := current.Slug
		if in.Body.Slug != nil && *in.Body.Slug != "" {
			candidate := strings.ToLower(strings.TrimSpace(*in.Body.Slug))
			if candidate != current.Slug {
				if _, err := deps.Queries.FindWorkspaceBySlug(ctx, candidate); err == nil {
					return nil, httpErr(apierrors.WsWorkspaceSlugAlreadyTaken)
				} else if !errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			}
			newSlug = candidate
		}

		if err := deps.Queries.UpdateWorkspaceFull(ctx, generated.UpdateWorkspaceFullParams{
			Name:     newName,
			Slug:     newSlug,
			PublicID: types.FromUUID(ws.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		updated := rowToWorkspaceFromFind(current)
		updated.Name = newName
		updated.Slug = newSlug
		updated.Role = string(ws.Role)
		return &PatchOutput{Body: updated}, nil
	}
}
