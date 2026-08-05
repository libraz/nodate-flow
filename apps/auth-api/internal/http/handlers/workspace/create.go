package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// Create handles POST /workspaces. The authenticated actor becomes the
// owner of the new workspace.
func Create(deps Deps) func(context.Context, *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
	return func(ctx context.Context, in *CreateWorkspaceInput) (*CreateWorkspaceOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
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

		tz := in.Body.Timezone
		if tz == "" {
			tz = region.DefaultTimezone
		} else if err := region.ValidateTimezone(tz); err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		country := in.Body.Country
		if err := region.ValidateCountry(country); err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		icon := sql.NullString{String: in.Body.IconURL, Valid: in.Body.IconURL != ""}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()
		txQueries := deps.Queries.WithTx(tx)

		wsID, err := txQueries.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
			PublicID:    pub,
			Slug:        slug,
			Name:        in.Body.Name,
			Description: desc,
			IconUrl:     icon,
			Timezone:    tz,
			Country:     sql.NullString{String: country, Valid: country != ""},
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Add the creator as owner through memberkit so their
		// personal calendar layer materialises in the same tx.
		if _, err := memberkit.AddWorkspaceMember(ctx, tx, memberkit.AddWorkspaceMemberArgs{
			WorkspaceID:              uint32(wsID), //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			UserID:                   uid,
			Role:                     memberkit.RoleOwner,
			EnsurePersonalCalendar:   true,
			SubscribeHolidayCalendar: country != "",
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "workspace.create",
			ActorID:      uid,
			WorkspaceID:  uint32(wsID), //#nosec G115 -- LastInsertId for workspaces.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			ResourceType: "workspace",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"slug": slug, "name": in.Body.Name},
		})

		out := &CreateWorkspaceOutput{Body: Workspace{
			ID:          pub.String(),
			Slug:        slug,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			IconURL:     in.Body.IconURL,
			Timezone:    tz,
			Country:     country,
			Role:        string(generated.WorkspaceMembersRoleOwner),
			CreatedAt:   time.Now().Unix(),
		}}
		return out, nil
	}
}
