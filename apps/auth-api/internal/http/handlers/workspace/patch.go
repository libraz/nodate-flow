package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// Patch handles PATCH /workspaces/{wsId}. Only workspace admins or owners
// may rename, re-slug, or update branding fields; the role check is
// enforced at the router layer via RequireWorkspaceRole.
func Patch(deps Deps) func(context.Context, *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
	return func(ctx context.Context, in *PatchWorkspaceInput) (*PatchWorkspaceOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		current, err := deps.Queries.FindWorkspaceByPublicId(ctx, types.FromUUID(ws.PublicID))
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
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
		if in.Body.Timezone != nil {
			if err := region.ValidateTimezone(*in.Body.Timezone); err != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			params.Timezone = sql.NullString{String: *in.Body.Timezone, Valid: true}
		}
		if in.Body.Country != nil {
			if err := region.ValidateCountry(*in.Body.Country); err != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			params.Country = sql.NullString{String: *in.Body.Country, Valid: *in.Body.Country != ""}
		}

		// The affected-row count is not the existence check here and
		// cannot be: MySQL counts changed rows, so submitting the values
		// the workspace already has reports zero. Existence is settled by
		// the workspace middleware before this runs, and the re-read
		// below would fail if the row had vanished in between.
		if _, err := deps.Queries.PatchWorkspace(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if actorID, ok := authn.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "workspace.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "workspace",
				ResourceID:   ws.PublicID.String(),
			})
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
