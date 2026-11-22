package projects

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/projectkit"
)

// Create handles POST /workspaces/{wsId}/projects.
func Create(deps Deps) func(context.Context, *CreateProjectInput) (*CreateProjectOutput, error) {
	return func(ctx context.Context, in *CreateProjectInput) (*CreateProjectOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		// The slug is used as sent. Its character set and length are
		// settled by the request schema, so there is nothing left here to
		// fold or trim, and folding would mean an uppercase slug landed on
		// a row the caller never named.
		slug := in.Body.Slug

		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		color := sql.NullString{String: in.Body.Color, Valid: in.Body.Color != ""}
		identifier := strings.ToUpper(strings.TrimSpace(in.Body.Identifier))
		identifierParam := sql.NullString{String: identifier, Valid: identifier != ""}
		userID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionUnauthorized)
		}
		var created projectkit.Project
		txErr := dbretry.InTx(ctx, deps.DB, "projects.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			var err error
			// The creator is enrolled as lead in the same transaction so
			// the project is visible to per-project ACL predicates and the
			// members grid without the workspace owner/admin escape hatch.
			created, err = projectkit.Create(ctx, tx, projectkit.CreateArgs{
				WorkspaceID: ws.ID,
				Slug:        slug,
				Identifier:  identifierParam,
				Name:        in.Body.Name,
				Description: desc,
				Color:       color,
				LeadUserID:  userID,
			})
			return err
		})
		if txErr != nil {
			switch {
			case errors.Is(txErr, projectkit.ErrSlugTaken):
				return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
			case errors.Is(txErr, projectkit.ErrIdentifierTaken):
				return nil, httpErr(apierrors.WsProjectIdentifierAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := created.PublicID

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "project.create",
			ActorID:      userID,
			WorkspaceID:  ws.ID,
			ResourceType: "project",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"slug": slug, "name": in.Body.Name},
		})

		return &CreateProjectOutput{Body: Project{
			ID:               pub.String(),
			WorkspaceID:      ws.PublicID.String(),
			Slug:             slug,
			Identifier:       identifier,
			Name:             in.Body.Name,
			Description:      in.Body.Description,
			Color:            in.Body.Color,
			FeaturePages:     true,
			FeatureTimeboxes: true,
			FeatureLenses:    true,
			FeatureCalendar:  true,
			CreatedAt:        handlerutil.NowUnix(),
		}}, nil
	}
}
