package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// Deps is the dependency bundle passed to each auth handler.
type Deps struct {
	Queries      *generated.Queries
	DB           *sql.DB
	JWT          *auth.JWTIssuer
	CookieSecure bool
}

const refreshCookieName = "nd_rt"
const refreshCookiePath = "/auth"
const refreshCookieTTL = 30 * 24 * time.Hour
const maxFailedBeforeLock = 5
const userAgentMaxLen = 255

// --- Input / Output types ---

// AuthTokens is the tokens envelope returned by register/login/refresh.
type AuthTokens struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt" doc:"Access token expiry, unix seconds"`
	UserID      string `json:"userId" doc:"User public id (UUID v7)"`
}

type RegisterInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email       string `json:"email" format:"email" maxLength:"254"`
		Password    string `json:"password" minLength:"8" maxLength:"256"`
		DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
		Locale      string `json:"locale,omitempty" maxLength:"10" required:"false"`
	}
}

type RegisterOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password"`
	}
}

type LoginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

type RefreshInput struct {
	UserAgent     string      `header:"User-Agent"`
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

type RefreshOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

type LogoutInput struct {
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

type LogoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Ok bool `json:"ok"`
	}
}

type MeBody struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"displayName"`
	Locale          string  `json:"locale"`
	ThemePreference string  `json:"themePreference" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,system"`
	AvatarURL       *string `json:"avatarUrl,omitempty"`
}

type MeOutput struct {
	Body MeBody
}

type ChangePasswordInput struct {
	Body struct {
		CurrentPassword string `json:"currentPassword" minLength:"1" maxLength:"256"`
		NewPassword     string `json:"newPassword" minLength:"8" maxLength:"256"`
	}
}

type ChangePasswordOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

type PatchMeInput struct {
	Body struct {
		DisplayName     *string `json:"displayName,omitempty" minLength:"1" maxLength:"100" required:"false"`
		Locale          *string `json:"locale,omitempty" maxLength:"10" required:"false"`
		ThemePreference *string `json:"themePreference,omitempty" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,system" required:"false"`
		AvatarURL       *string `json:"avatarUrl,omitempty" maxLength:"1024" required:"false"`
	}
}

type PatchMeOutput struct {
	Body MeBody
}

// --- Helpers ---

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

func newRefreshCookie(token string, secure bool) http.Cookie {
	return http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   int(refreshCookieTTL.Seconds()),
	}
}

func clearedRefreshCookie(secure bool) http.Cookie {
	return http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   -1,
	}
}

func refreshCookieSameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func truncateUserAgent(ua string) string {
	if len(ua) > userAgentMaxLen {
		return ua[:userAgentMaxLen]
	}
	return ua
}

func issueTokens(ctx context.Context, deps Deps, userID uint32, userPub types.PublicID, userAgent string) (AuthTokens, string, error) {
	sessPub := types.New()
	access, exp, err := deps.JWT.Sign(userPub, sessPub)
	if err != nil {
		return AuthTokens{}, "", httpErr(apierrors.InternalUnexpected)
	}
	refresh, refreshHash, err := auth.GenerateRefresh()
	if err != nil {
		return AuthTokens{}, "", httpErr(apierrors.InternalUnexpected)
	}
	if _, err := deps.Queries.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    sessPub,
		UserID:      userID,
		RefreshHash: refreshHash,
		UserAgent:   sql.NullString{String: truncateUserAgent(userAgent), Valid: userAgent != ""},
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

// --- Handlers ---

// Register handles POST /auth/register.
func Register(deps Deps) func(context.Context, *RegisterInput) (*RegisterOutput, error) {
	return func(ctx context.Context, in *RegisterInput) (*RegisterOutput, error) {
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		if len(in.Body.Password) < 8 {
			return nil, httpErr(apierrors.AuthRegisterPasswordTooWeak)
		}
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
			ThemePreference: generated.UsersThemePreferenceSystem,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		identPub := types.New()
		if _, err := deps.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
			PublicID:     identPub,
			UserID:       uint32(uid),
			Provider:     generated.IdentitiesProviderLocal,
			Subject:      email,
			PasswordHash: sql.NullString{String: hash, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tokens, refresh, err := issueTokens(ctx, deps, uint32(uid), userPub, in.UserAgent)
		if err != nil {
			return nil, err
		}
		slog.InfoContext(ctx, "auth.register.success", "userId", userPub.String(), "email", email)
		return &RegisterOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}

// Login handles POST /auth/login.
func Login(deps Deps) func(context.Context, *LoginInput) (*LoginOutput, error) {
	return func(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		row, err := deps.Queries.FindLocalIdentityByEmail(ctx, email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Perform a dummy argon2id verification to equalise timing
				// with the real path, preventing user enumeration.
				_, _ = auth.VerifyPassword(auth.DummyHash(), in.Body.Password)
				slog.WarnContext(ctx, "auth.login.failure", "email", email, "reason", "user_not_found")
				return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if row.LockedUntilAt.Valid && row.LockedUntilAt.Time.After(time.Now()) {
			slog.WarnContext(ctx, "auth.login.failure", "email", email, "reason", "account_locked")
			return nil, httpErr(apierrors.AuthLoginAccountLocked)
		}
		if !row.PasswordHash.Valid {
			slog.WarnContext(ctx, "auth.login.failure", "email", email, "reason", "no_password")
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		ok, verr := auth.VerifyPassword(row.PasswordHash.String, in.Body.Password)
		if verr != nil || !ok {
			bumpFailed(ctx, deps, row)
			if row.FailedAttempts+1 >= maxFailedBeforeLock {
				slog.WarnContext(ctx, "auth.login.failure", "email", email, "reason", "rate_limited", "attempts", row.FailedAttempts+1)
				return nil, httpErr(apierrors.AuthLoginRateLimited)
			}
			slog.WarnContext(ctx, "auth.login.failure", "email", email, "reason", "invalid_credentials")
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		_ = deps.Queries.ResetIdentityFailedAttempts(ctx, row.ID)
		_ = deps.Queries.UpdateUserLastLoginAt(ctx, row.UserID)

		tokens, refresh, err := issueTokens(ctx, deps, row.UserID, row.UserPublicID, in.UserAgent)
		if err != nil {
			return nil, err
		}
		slog.InfoContext(ctx, "auth.login.success", "userId", row.UserPublicID.String(), "email", email)
		return &LoginOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}

func bumpFailed(ctx context.Context, deps Deps, row generated.FindLocalIdentityByEmailRow) {
	next := row.FailedAttempts + 1
	var lock sql.NullTime
	if next >= maxFailedBeforeLock {
		lock = sql.NullTime{Time: time.Now().Add(15 * time.Minute), Valid: true}
	}
	_ = deps.Queries.UpdateIdentityFailedAttempts(ctx, generated.UpdateIdentityFailedAttemptsParams{
		FailedAttempts: next,
		LockedUntilAt:  lock,
		ID:             row.ID,
	})
}

// Refresh handles POST /auth/refresh.
func Refresh(deps Deps) func(context.Context, *RefreshInput) (*RefreshOutput, error) {
	return func(ctx context.Context, in *RefreshInput) (*RefreshOutput, error) {
		plain := in.RefreshCookie.Value
		if plain == "" {
			return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
		}
		hash := auth.HashOpaque(plain)
		sess, err := deps.Queries.FindSessionByRefreshHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if sess.ExpiresAt.Before(time.Now()) {
			return nil, httpErr(apierrors.AuthTokenRefreshExpired)
		}

		newPlain, newHash, err := auth.GenerateRefresh()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		newExp := time.Now().Add(refreshCookieTTL)
		if err := deps.Queries.RotateSessionRefreshHash(ctx, generated.RotateSessionRefreshHashParams{
			RefreshHash: newHash,
			ExpiresAt:   newExp,
			ID:          sess.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub, qerr := deps.Queries.FindUserPublicIdById(ctx, sess.UserID)
		if qerr != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		access, exp, err := deps.JWT.Sign(pub, sess.PublicID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		slog.InfoContext(ctx, "auth.token.refresh", "userId", pub.String())
		return &RefreshOutput{
			SetCookie: newRefreshCookie(newPlain, deps.CookieSecure),
			Body: AuthTokens{
				AccessToken: access,
				ExpiresAt:   exp.Unix(),
				UserID:      pub.String(),
			},
		}, nil
	}
}

// Logout handles POST /auth/logout.
func Logout(deps Deps) func(context.Context, *LogoutInput) (*LogoutOutput, error) {
	return func(ctx context.Context, in *LogoutInput) (*LogoutOutput, error) {
		out := &LogoutOutput{
			SetCookie: clearedRefreshCookie(deps.CookieSecure),
		}
		out.Body.Ok = true
		plain := in.RefreshCookie.Value
		if plain == "" {
			return out, nil
		}
		hash := auth.HashOpaque(plain)
		sess, err := deps.Queries.FindSessionByRefreshHash(ctx, hash)
		if err != nil {
			return out, nil
		}
		_ = deps.Queries.RevokeSession(ctx, generated.RevokeSessionParams{
			UserID:   sess.UserID,
			PublicID: sess.PublicID,
		})
		slog.InfoContext(ctx, "auth.logout", "sessionId", sess.PublicID.String())
		return out, nil
	}
}

// Me handles GET /me.
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
		return &MeOutput{Body: rowToMe(row)}, nil
	}
}

// PatchMe handles PATCH /me.
func PatchMe(deps Deps) func(context.Context, *PatchMeInput) (*PatchMeOutput, error) {
	return func(ctx context.Context, in *PatchMeInput) (*PatchMeOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		params := generated.PatchMeParams{ID: uid}
		if in.Body.DisplayName != nil {
			params.DisplayName = sql.NullString{String: *in.Body.DisplayName, Valid: true}
		}
		if in.Body.Locale != nil {
			params.Locale = sql.NullString{String: *in.Body.Locale, Valid: true}
		}
		if in.Body.ThemePreference != nil {
			params.ThemePreference = generated.NullUsersThemePreference{
				UsersThemePreference: generated.UsersThemePreference(*in.Body.ThemePreference),
				Valid:                true,
			}
		}
		if in.Body.AvatarURL != nil {
			params.AvatarUrl = sql.NullString{String: *in.Body.AvatarURL, Valid: true}
		}

		if err := deps.Queries.PatchMe(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &PatchMeOutput{Body: rowToMe(row)}, nil
	}
}

// ChangePassword handles PATCH /me/password.
func ChangePassword(deps Deps) func(context.Context, *ChangePasswordInput) (*ChangePasswordOutput, error) {
	return func(ctx context.Context, in *ChangePasswordInput) (*ChangePasswordOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		identity, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("No local identity found")
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if !identity.PasswordHash.Valid {
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}

		ok, verr := auth.VerifyPassword(identity.PasswordHash.String, in.Body.CurrentPassword)
		if verr != nil || !ok {
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}

		newHash, err := auth.HashPassword(in.Body.NewPassword)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		err = deps.Queries.UpdateIdentityPasswordHash(ctx, generated.UpdateIdentityPasswordHashParams{
			PasswordHash: sql.NullString{String: newHash, Valid: true},
			ID:           identity.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		slog.InfoContext(ctx, "auth.password.changed", "userId", uid)
		out := &ChangePasswordOutput{}
		out.Body.Updated = true
		return out, nil
	}
}

func rowToMe(row generated.FindUserProfileByIdRow) MeBody {
	var avatar *string
	if row.AvatarUrl.Valid {
		s := row.AvatarUrl.String
		avatar = &s
	}
	return MeBody{
		ID:              row.PublicID.String(),
		Email:           row.Email,
		DisplayName:     row.DisplayName,
		Locale:          row.Locale,
		ThemePreference: string(row.ThemePreference),
		AvatarURL:       avatar,
	}
}
