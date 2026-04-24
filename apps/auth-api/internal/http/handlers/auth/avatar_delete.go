package auth

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// AvatarDeleteOutput returns the updated /me profile so the client can
// refresh its local user state without an extra round-trip.
type AvatarDeleteOutput struct {
	Body MeBody
}

// AvatarDelete handles DELETE /me/avatar. It nulls out users.avatar_url
// and removes the previous object from the store when it was uploaded
// via POST /me/avatar (external OIDC URLs are not stored by us and are
// simply forgotten from the column).
//
// The endpoint is idempotent: a user with no avatar receives a 200 with
// AvatarURL == nil.
func AvatarDelete(deps Deps) func(context.Context, *struct{}) (*AvatarDeleteOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*AvatarDeleteOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		prev, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		// Best-effort removal of the previous object. Missing storage
		// client or delete failures are logged but never bubble up:
		// the DB clear below is the source of truth.
		if prev.AvatarUrl.Valid && !isExternalAvatarURL(prev.AvatarUrl.String) && deps.Storage != nil {
			if err := deps.Storage.RemoveObject(ctx, prev.AvatarUrl.String); err != nil {
				slog.WarnContext(ctx, "avatar delete: object delete failed", "err", err, "key", prev.AvatarUrl.String)
			}
		}

		if err := deps.Queries.ClearMyAvatarURL(ctx, uid); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "user.avatar.delete",
			ActorID:      uid,
			ResourceType: "user",
		})

		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		me := rowToMe(row, deps.PublicBaseURL)
		if isAdmin, aerr := deps.Queries.AdminIsInstanceAdmin(ctx, uid); aerr == nil {
			me.IsInstanceAdmin = isAdmin
		}
		return &AvatarDeleteOutput{Body: me}, nil
	}
}
