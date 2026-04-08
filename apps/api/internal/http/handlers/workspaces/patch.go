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
// may rename, re-slug, or update branding fields; the role check is
// enforced at the router layer via RequireWorkspaceRole. Only fields
// explicitly supplied in the body are touched.
func Patch(deps Deps) func(context.Context, *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
	return func(ctx context.Context, in *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
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

		params := generated.PatchWorkspaceParams{PublicID: types.FromUUID(ws.PublicID)}

		if in.Body.Name != nil && *in.Body.Name != "" {
			params.Name = sql.NullString{String: *in.Body.Name, Valid: true}
		}
		if in.Body.Slug != nil && *in.Body.Slug != "" {
			candidate := strings.ToLower(strings.TrimSpace(*in.Body.Slug))
			if candidate != current.Slug {
				if _, err := deps.Queries.FindWorkspaceBySlug(ctx, candidate); err == nil {
					return nil, httpErr(apierrors.WsWorkspaceSlugAlreadyTaken)
				} else if !errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			}
			params.Slug = sql.NullString{String: candidate, Valid: true}
		}
		if in.Body.Description != nil {
			params.Description = sql.NullString{String: *in.Body.Description, Valid: true}
		}
		if in.Body.IconURL != nil {
			params.IconUrl = sql.NullString{String: *in.Body.IconURL, Valid: true}
		}

		if err := deps.Queries.PatchWorkspace(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		refreshed, err := deps.Queries.FindWorkspaceByPublicId(ctx, types.FromUUID(ws.PublicID))
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		updated := rowToWorkspaceFromFind(refreshed)
		updated.Role = string(ws.Role)
		return &PatchWorkspaceOutput{Body: updated}, nil
	}
}
