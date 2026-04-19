package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Register handles POST /auth/register and creates a new local-password
// user account plus its initial session.
func Register(deps Deps) func(context.Context, *RegisterInput) (*RegisterOutput, error) {
	return func(ctx context.Context, in *RegisterInput) (*RegisterOutput, error) {
		if !deps.RegistrationOpen {
			return nil, httpErr(apierrors.AuthRegisterInstanceRegistrationDisabled)
		}
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

		tokens, refresh, err := issueTokens(ctx, deps, uint32(uid), userPub, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &RegisterOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}

// issueTokens signs an access JWT and creates a new session row. It
// returns the JSON-body AuthTokens envelope plus the freshly-minted
// plaintext refresh token, which the caller must attach to the
// response via a Set-Cookie header (never in the JSON body).
func issueTokens(ctx context.Context, deps Deps, userID uint32, userPub types.PublicID, userAgent, ipAddress string) (AuthTokens, string, error) {
	sessPub := types.New()
	access, exp, err := deps.JWT.Sign(userPub, sessPub)
	if err != nil {
		return AuthTokens{}, "", httpErr(apierrors.InternalUnexpected)
	}
	refresh, refreshHash, err := auth.GenerateRefresh()
	if err != nil {
		return AuthTokens{}, "", httpErr(apierrors.InternalUnexpected)
	}
	if _, err := deps.Sessions.Create(ctx, sessionstore.CreateParams{
		PublicID:    sessPub,
		UserID:      userID,
		RefreshHash: refreshHash,
		UserAgent:   truncateUserAgent(userAgent),
		IPAddress:   ipAddress,
		ExpiresAt:   time.Now().Add(refreshCookieTTL),
	}); err != nil {
		return AuthTokens{}, "", httpErr(apierrors.InternalUnexpected)
	}
	return AuthTokens{
		AccessToken: access,
		ExpiresAt:   exp.Unix(),
		UserID:      userPub.String(),
	}, refresh, nil
}

// userAgentMaxLen caps the stored User-Agent string. The sessions
// table column is sized for this upper bound.
const userAgentMaxLen = 255

func truncateUserAgent(ua string) string {
	if len(ua) > userAgentMaxLen {
		return ua[:userAgentMaxLen]
	}
	return ua
}
