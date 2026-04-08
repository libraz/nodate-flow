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
		if _, err := deps.Queries.CreateProject(ctx, generated.CreateProjectParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			Slug:        slug,
			Name:        in.Body.Name,
			Description: desc,
			Color:       color,
		}); err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry {
				return nil, httpErr(apierrors.WsProjectSlugAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &CreateProjectOutput{Body: Project{
			ID:          pub.String(),
			Slug:        slug,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			Color:       in.Body.Color,
			CreatedAt:   time.Now(),
		}}, nil
	}
}
