// Change-password handler for /settings/security. Verifies the
// caller's current password, writes a fresh Argon2id hash, and
// revokes every other session as a side effect so stolen refresh
// tokens cannot outlive a password reset.
package auth

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ChangePassword handles POST /me/password.
func ChangePassword(deps Deps) func(context.Context, *ChangePasswordInput) (*ChangePasswordOutput, error) {
	return func(ctx context.Context, in *ChangePasswordInput) (*ChangePasswordOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		if len(in.Body.NewPassword) < deps.minPwLen() {
			return nil, httpErr(apierrors.AuthPasswordTooWeak)
		}
		row, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthPasswordNoLocalIdentity, apierrors.InternalUnexpected))
		}
		if !row.PasswordHash.Valid {
			return nil, httpErr(apierrors.AuthPasswordNoLocalIdentity)
		}
		okPw, err := auth.VerifyPassword(row.PasswordHash.String, in.Body.CurrentPassword)
		if err != nil || !okPw {
			return nil, httpErr(apierrors.AuthPasswordCurrentMismatch)
		}
		newHash, err := auth.HashPassword(in.Body.NewPassword)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.UpdateIdentityPasswordHash(ctx, generated.UpdateIdentityPasswordHashParams{
			PasswordHash: sql.NullString{String: newHash, Valid: true},
			ID:           row.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Revoke every session except the one on the current request so
		// the caller stays signed in but any stolen refresh token dies
		// at the same moment the password rotates. If the current
		// session cannot be identified (no cookie) we fall back to
		// revoking everything — the caller will be forced to sign in
		// again with the new password, which is acceptable.
		currentPub, hasCurrent := currentSessionPublicID(ctx, deps, in.RefreshCookie.Value)
		before, err := deps.Sessions.ListActive(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		revoked := 0
		if hasCurrent {
			if err := deps.Sessions.RevokeAllExcept(ctx, uid, currentPub); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, s := range before {
				if s.PublicID != currentPub {
					revoked++
				}
			}
		} else {
			for _, s := range before {
				if err := deps.Sessions.Revoke(ctx, uid, s.PublicID); err != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				revoked++
			}
		}

		out := &ChangePasswordOutput{}
		out.Body.Ok = true
		out.Body.OtherSessionsRevoked = revoked
		return out, nil
	}
}
