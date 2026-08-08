package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// Register handles POST /auth/register and creates a new local-password
// user account plus its initial session.
func Register(deps Deps) func(context.Context, *RegisterInput) (*RegisterOutput, error) {
	return func(ctx context.Context, in *RegisterInput) (*RegisterOutput, error) {
		if !deps.RegistrationOpen {
			return nil, httpErr(apierrors.AuthRegisterInstanceRegistrationDisabled)
		}
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		if len(in.Body.Password) < deps.minPwLen() {
			return nil, httpErr(apierrors.AuthRegisterPasswordTooWeak)
		}
		// Conflict check.
		if _, err := deps.Queries.FindUserByEmail(ctx, email); err == nil {
			return nil, httpErr(apierrors.AuthRegisterEmailAlreadyTaken)
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.ErrorContext(ctx, "register: email lookup failed", "error", err)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		hash, err := auth.HashPassword(in.Body.Password)
		if err != nil {
			slog.ErrorContext(ctx, "register: password hash failed", "error", err)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		userPub := types.New()
		locale := in.Body.Locale
		if locale == "" {
			locale = "en"
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
		uid, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
			PublicID:        userPub,
			Email:           email,
			DisplayName:     in.Body.DisplayName,
			Locale:          locale,
			Timezone:        tz,
			Country:         sql.NullString{String: country, Valid: country != ""},
			ThemePreference: generated.UsersThemePreference("system"),
		})
		if err != nil {
			// The lookup above is advisory only: two sign-ups for the same
			// address can both pass it and only uniq_users_email decides
			// which one exists. Reporting the loser as an internal error
			// tells someone who typed their own address that the service
			// is broken, and hides the one thing they can act on — that
			// the account is already there and the next step is signing in.
			if handlerutil.IsDuplicateEntry(err) {
				return nil, httpErr(apierrors.AuthRegisterEmailAlreadyTaken)
			}
			slog.ErrorContext(ctx, "register: create user failed", "error", err)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		identPub := types.New()
		if _, err := deps.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
			PublicID:     identPub,
			UserID:       uint32(uid), //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
			Provider:     generated.IdentitiesProvider("local"),
			Subject:      email,
			PasswordHash: sql.NullString{String: hash, Valid: true},
		}); err != nil {
			// uniq_identities_provider_subject is the same race one step
			// later: the local identity is keyed by the address, so a
			// concurrent sign-up that reached this insert first owns it.
			if handlerutil.IsDuplicateEntry(err) {
				return nil, httpErr(apierrors.AuthRegisterEmailAlreadyTaken)
			}
			slog.ErrorContext(ctx, "register: create identity failed", "error", err)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tokens, refresh, err := IssueTokens(ctx, deps, uint32(uid), userPub, in.UserAgent, authn.ClientIPFromContext(ctx)) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		if err != nil {
			return nil, err
		}
		return &RegisterOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}
