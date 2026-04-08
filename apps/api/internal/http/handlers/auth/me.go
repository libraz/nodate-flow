package auth

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// Me handles GET /me. It reads the actor user id injected by the auth
// middleware and returns the matching user profile.
func Me(deps Deps) func(context.Context, *struct{}) (*MeOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		out := &MeOutput{}
		out.Body.ID = row.PublicID.String()
		out.Body.Email = row.Email
		out.Body.DisplayName = row.DisplayName
		out.Body.Locale = row.Locale
		return out, nil
	}
}
