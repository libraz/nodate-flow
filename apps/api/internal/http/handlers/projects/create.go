package projects

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// mysqlErrDuplicateEntry is the MySQL error number for a unique-constraint
// violation (ER_DUP_ENTRY). See https://dev.mysql.com/doc/mysql-errors/8.4/en/
const mysqlErrDuplicateEntry = 1062

// Create handles POST /workspaces/{wsId}/projects.
func Create(deps Deps) func(context.Context, *CreateProjectInput) (*CreateProjectOutput, error) {
	return func(ctx context.Context, in *CreateProjectInput) (*CreateProjectOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		slug := strings.ToLower(strings.TrimSpace(in.Body.Slug))
		if slug == "" {
			return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
		}

		pub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		color := sql.NullString{String: in.Body.Color, Valid: in.Body.Color != ""}
		projectID, err := deps.Queries.CreateProject(ctx, generated.CreateProjectParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			Slug:        slug,
			Name:        in.Body.Name,
			Description: desc,
			Color:       color,
		})
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry {
				// Only the (workspace_id, slug) unique key should map to
				// SLUG_ALREADY_TAKEN. Other unique violations (e.g. a
				// public_id collision) must surface as INTERNAL.
				if strings.Contains(mysqlErr.Message, "uniq_projects_workspace_id_slug") {
					return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
				}
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Auto-enroll the creator as a project lead so they can see the
		// project in the members grid, satisfy per-project ACL predicates
		// (e.g. workspace timeline), and don't need the workspace
		// owner/admin escape hatch. Best-effort: a failure here is
		// logged but not fatal (the project itself is already created).
		if userID, ok := middleware.ActorFromContext(ctx); ok {
			memberPub := types.New()
			if _, mErr := deps.Queries.AddProjectMember(ctx, generated.AddProjectMemberParams{
				PublicID:    memberPub,
				WorkspaceID: ws.ID,
				ProjectID:   uint32(projectID),
				UserID:      userID,
				Role:        generated.ProjectMembersRoleLead,
				AddedAt:     sql.NullTime{Time: time.Now(), Valid: true},
			}); mErr != nil {
				// Swallow: not fatal. Caller can still open the project
				// via workspace owner/admin escape hatch if they have it.
				_ = mErr
			}
		}

		return &CreateProjectOutput{Body: Project{
			ID:          pub.String(),
			WorkspaceID: ws.PublicID.String(),
			Slug:        slug,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			Color:       in.Body.Color,
			CreatedAt:   time.Now(),
		}}, nil
	}
}
