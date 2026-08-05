package admin

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
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

		// Atomic bootstrap: the conditional INSERT...SELECT only writes a
		// row when no active admin exists yet, evaluated as a single
		// statement. This closes the check-then-act TOCTOU where two
		// concurrent /admin/setup calls could each pass a separate
		// existence check and both create an admin. The loser of the race
		// (and any later caller) sees zero affected rows and gets a clean
		// already-initialized error.
		affected, err := deps.Queries.AdminBootstrapFirstInstanceAdmin(ctx, generated.AdminBootstrapFirstInstanceAdminParams{
			PublicID:        types.New(),
			UserID:          uid,
			GrantedByUserID: sql.NullInt32{},
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if affected == 0 {
			return nil, httpErr(apierrors.InstanceSetupAlreadyInitialized)
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
