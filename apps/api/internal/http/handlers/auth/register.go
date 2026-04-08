package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Register handles POST /auth/register and creates a new local-password
// user account plus its initial session.
func Register(deps Deps) func(context.Context, *RegisterInput) (*RegisterOutput, error) {
	return func(ctx context.Context, in *RegisterInput) (*RegisterOutput, error) {
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		if len(in.Body.Password) < 8 {
			return nil, httpErr(apierrors.AuthRegisterPasswordTooWeak)
		}
		// Conflict check.
		if _, err := deps.Queries.FindUserByEmail(ctx, email); err == nil {
			return nil, httpErr(apierrors.AuthRegisterEmailAlreadyTaken)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		hash, err := auth.HashPassword(in.Body.Password)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		userPub := types.New()
		locale := in.Body.Locale
		if locale == "" {
			locale = "en"
		}
		uid, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
			PublicID:        userPub,
			Email:           email,
			DisplayName:     in.Body.DisplayName,
			Locale:          locale,
			ThemePreference: generated.UsersThemePreference("system"),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		identPub := types.New()
		if _, err := deps.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
			PublicID:     identPub,
			UserID:       uint32(uid),
			Provider:     generated.IdentitiesProvider("local"),
			Subject:      email,
			PasswordHash: sql.NullString{String: hash, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tokens, err := issueTokens(ctx, deps, uint32(uid), userPub)
		if err != nil {
			return nil, err
		}
		return &RegisterOutput{Body: tokens}, nil
	}
}

// issueTokens signs an access JWT and creates a new session row, returning
// the AuthTokens envelope shared by register/login/refresh.
func issueTokens(ctx context.Context, deps Deps, userID uint32, userPub types.PublicID) (AuthTokens, error) {
	access, exp, err := deps.JWT.Sign(userPub)
	if err != nil {
		return AuthTokens{}, httpErr(apierrors.InternalUnexpected)
	}
	refresh, refreshHash, err := auth.GenerateRefresh()
	if err != nil {
		return AuthTokens{}, httpErr(apierrors.InternalUnexpected)
	}
	sessPub := types.New()
	if _, err := deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    sessPub,
		UserID:      userID,
		RefreshHash: refreshHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		return AuthTokens{}, httpErr(apierrors.InternalUnexpected)
	}
	return AuthTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp.Unix(),
		UserID:       userPub.String(),
	}, nil
}
