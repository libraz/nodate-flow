package projects

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// Patch handles PATCH /projects/{prjId}.
func Patch(deps Deps) func(context.Context, *PatchProjectInput) (*PatchProjectOutput, error) {
	return func(ctx context.Context, in *PatchProjectInput) (*PatchProjectOutput, error) {
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		current, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, types.FromUUID(prj.PublicID))
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}

		newName := current.Name
		if in.Body.Name != nil && *in.Body.Name != "" {
			newName = *in.Body.Name
		}
		newSlug := current.Slug
		if in.Body.Slug != nil && *in.Body.Slug != "" {
			newSlug = strings.ToLower(strings.TrimSpace(*in.Body.Slug))
		}
		newDesc := current.Description
		if in.Body.Description != nil {
			newDesc = sql.NullString{String: *in.Body.Description, Valid: *in.Body.Description != ""}
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "project.update",
				ActorID:      actorID,
				WorkspaceID:  current.WorkspaceID,
				ResourceType: "project",
				ResourceID:   prj.PublicID.String(),
			})
		}

		if err := deps.Queries.UpdateProjectFull(ctx, generated.UpdateProjectFullParams{
			Name:        newName,
			Slug:        newSlug,
			Description: newDesc,
			WorkspaceID: current.WorkspaceID,
			PublicID:    types.FromUUID(prj.PublicID),
		}); err != nil {
			if handlerutil.IsDuplicateEntry(err) {
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && strings.Contains(mysqlErr.Message, "uniq_projects_workspace_id_slug") {
					return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
				}
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		updated := rowToProjectFromFind(current)
		updated.Name = newName
		updated.Slug = newSlug
		updated.Description = nullStr(newDesc)
		return &PatchProjectOutput{Body: updated}, nil
	}
}
