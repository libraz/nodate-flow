package admin

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Setup handles POST /admin/setup. It promotes the calling user to the first
// instance administrator. This endpoint is protected by RequireAuth only (not
// RequireInstanceAdmin) and returns 409 if an admin already exists.
func Setup(deps Deps) func(context.Context, *struct{}) (*SetupOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*SetupOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		hasAdmin, err := deps.Queries.AdminCheckInstanceAdminExists(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if hasAdmin {
			return nil, httpErr(apierrors.InstanceSetupAlreadyInitialized)
		}

		_, err = deps.Queries.AdminGrantInstanceAdmin(ctx, generated.AdminGrantInstanceAdminParams{
			PublicID:        types.New(),
			UserID:          uid,
			GrantedByUserID: sql.NullInt32{},
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.setup",
			ActorID:      uid,
			ResourceType: "user",
		})

		out := &SetupOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
