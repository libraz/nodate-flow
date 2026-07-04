// Package auth contains Huma operation handlers for the authentication
// endpoints: register, login, refresh, logout, OIDC, and /me.
//
//nolint:revive // DTO names intentionally keep auth prefixes for stable generated OpenAPI schema names.
package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

// GithubExchanger is the narrow contract used by the GitHub OIDC
// start/callback handlers. The production implementation is
// *auth.GithubOAuthClient; tests inject a fake to avoid real HTTP.
type GithubExchanger interface {
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code, nonce string) (*auth.GithubClaims, error)
}

// MicrosoftExchanger is the narrow contract used by the Microsoft OIDC
// start/callback handlers. The production implementation is
// *auth.MicrosoftOIDCClient; tests inject a fake to avoid the real
// discovery / token-exchange round trip.
type MicrosoftExchanger interface {
	AuthCodeURL(ctx context.Context, state, nonce string) (string, error)
	Exchange(ctx context.Context, code, expectedNonce string) (*auth.MicrosoftClaims, error)
}

// Deps is the dependency bundle passed to each handler.
type Deps struct {
	DB       *sql.DB
	Queries  *generated.Queries
	Sessions sessionstore.Store
	JWT      *auth.JWTIssuer
	OIDC     *auth.OIDCClient
	// OIDCGithub is the GitHub OAuth2 client for login. Nil when the
	// NF_AUTH_GITHUB_OIDC_CLIENT_ID env is unset. Declared as an
	// interface so unit tests can substitute a fake exchanger.
	OIDCGithub GithubExchanger
	// OIDCMicrosoft is the Microsoft OIDC client for login. Nil when
	// the NF_AUTH_MICROSOFT_OIDC_CLIENT_ID env is unset. Declared as an
	// interface so unit tests can substitute a fake exchanger.
	OIDCMicrosoft MicrosoftExchanger
	// MicrosoftAllowedTenantIDs is the tenant allowlist configured by
	// NF_AUTH_MICROSOFT_OIDC_ALLOWED_TENANTS. The real exchanger
	// validates it after id_token verification; handlers also enforce it
	// so test doubles cannot bypass the boundary.
	MicrosoftAllowedTenantIDs []string
	// Cipher encrypts/decrypts TOTP secrets. Nil when NF_SECRET_KEY is
	// unset; the TOTP endpoints return AUTH.TOTP.NOT_CONFIGURED in that
	// case so the rest of the api still boots.
	Cipher *crypto.Cipher
	// CookieSecure toggles the Secure flag on the refresh cookie. It
	// defaults to true in production; local http dev can disable it via
	// NF_COOKIE_SECURE=false.
	CookieSecure bool
	// RegistrationOpen controls whether new user sign-up is allowed.
	// When false, POST /auth/register returns 403.
	RegistrationOpen bool
	// OAuthAllowedDomains / OAuthAllowedEmails carry the opt-in OAuth/OIDC
	// sign-in allowlist, normalized (lower-cased, trimmed, "@" stripped
	// from domains) in config.Load. When both are empty the allowlist is
	// inactive and any verified email may sign in. Evaluated by
	// isSignInEmailAllowed in the OIDC callback handlers.
	OAuthAllowedDomains []string
	OAuthAllowedEmails  []string
	// Audit records audit log entries for sensitive auth operations.
	// Optional: nil disables audit logging. Declared as audit.Sink so
	// tests can substitute an in-memory capture.
	Audit audit.Sink
	// EmailSender sends transactional emails (magic link). Nil when
	// SMTP is not configured.
	EmailSender email.Sender
	// AccountsWebURL is the origin of the accounts-web frontend, used
	// to build magic link verification URLs.
	AccountsWebURL string
	// MinPasswordLength is the minimum password length for registration
	// and password changes. Defaults to 8 when zero.
	MinPasswordLength int
	// Storage is the S3-compatible object store client used by the
	// avatar upload/download handlers. Nil when NF_S3_ENDPOINT is
	// unset; the handlers return AUTH.AVATAR.STORAGE_UNAVAILABLE in
	// that case so the rest of the api still boots.
	Storage *storage.Client
	// PublicBaseURL is the externally-visible origin of the auth-api,
	// used to build proxy URLs returned by /me (e.g.
	// "https://auth.example.com/avatars/{userPublicId}?v=...").
	PublicBaseURL string
}

// minPwLen returns the effective minimum password length.
func (d Deps) minPwLen() int {
	if d.MinPasswordLength > 0 {
		return d.MinPasswordLength
	}
	return 8
}

// isSignInEmailAllowed reports whether the given verified email may sign
// in under the instance's opt-in OAuth/OIDC allowlist.
//
// When both OAuthAllowedDomains and OAuthAllowedEmails are empty the
// allowlist is inactive and every email is allowed, preserving the
// default open sign-in behavior. Otherwise the email is allowed when its
// lower-cased form matches an entry in OAuthAllowedEmails, OR its domain
// (the part after the final "@") matches an entry in OAuthAllowedDomains.
// A malformed address with no "@" is rejected once any allowlist is set.
// The allowlist entries are already normalized by config.Load.
func (d Deps) isSignInEmailAllowed(email string) bool {
	if len(d.OAuthAllowedDomains) == 0 && len(d.OAuthAllowedEmails) == 0 {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range d.OAuthAllowedEmails {
		if normalized == allowed {
			return true
		}
	}
	at := strings.LastIndex(normalized, "@")
	if at < 0 || at == len(normalized)-1 {
		return false
	}
	domain := normalized[at+1:]
	for _, allowed := range d.OAuthAllowedDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}

// CapabilitiesOutput is the response for GET /auth/capabilities.
type CapabilitiesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         CapabilitiesBody
}

// CapabilitiesBody describes which authentication methods are available
// on this instance. Only boolean flags are exposed — no client IDs,
// secrets, or internal configuration details.
type CapabilitiesBody struct {
	// PasswordLogin is always true; local password auth cannot be disabled.
	PasswordLogin bool `json:"passwordLogin"`
	// OIDCGoogle indicates whether Google SSO is configured.
	OIDCGoogle bool `json:"oidcGoogle"`
	// OIDCGithub indicates whether GitHub SSO is configured.
	OIDCGithub bool `json:"oidcGithub"`
	// OIDCMicrosoft indicates whether Microsoft SSO is configured.
	OIDCMicrosoft bool `json:"oidcMicrosoft"`
	// MagicLink indicates whether passwordless magic-link login is available.
	MagicLink bool `json:"magicLink"`
	// Totp indicates whether TOTP 2FA enrollment is available.
	Totp bool `json:"totp"`
	// RegistrationOpen indicates whether self-service signup is allowed.
	RegistrationOpen bool `json:"registrationOpen"`
}

// RegisterInput is the body for POST /auth/register.
type RegisterInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email       string `json:"email" format:"email" maxLength:"254"`
		Password    string `json:"password" minLength:"8" maxLength:"256"`
		DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
		Locale      string `json:"locale,omitempty" maxLength:"10"`
		// Timezone is an optional IANA identifier. Defaults to "UTC" when
		// omitted. Independent of Locale.
		Timezone string `json:"timezone,omitempty" maxLength:"64"`
		// Country is an optional ISO 3166-1 alpha-2 code. Drives the
		// initial holiday subscription. Independent of Locale.
		Country string `json:"country,omitempty" pattern:"^$|^[A-Z]{2}$"`
	}
}

// AuthTokens is the tokens envelope returned by register/login/refresh.
// The refresh token is intentionally NOT part of this struct: it is
// delivered as an httpOnly Set-Cookie header instead.
type AuthTokens struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt" doc:"Access token expiry, unix seconds"`
	UserID      string `json:"userId" doc:"User public id (UUID v7)"`
}

// RegisterOutput is the response for POST /auth/register.
type RegisterOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// LoginInput is the body for POST /auth/login.
type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password"`
	}
}

// LoginBody is a discriminated envelope: when step="complete" the
// accessToken/expiresAt/userId fields are populated and the refresh
// cookie is set on the response; when step="totp_required" only
// challengeToken is populated and the client must call
// POST /auth/login/totp to finish signing in.
type LoginBody struct {
	Step           string `json:"step" enum:"complete,totp_required"`
	AccessToken    string `json:"accessToken,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty" doc:"Access token expiry, unix seconds"`
	UserID         string `json:"userId,omitempty" doc:"User public id (UUID v7)"`
	ChallengeToken string `json:"challengeToken,omitempty" doc:"Short-lived token to present on /auth/login/totp"`
}

// LoginOutput is the response for POST /auth/login. SetCookie is
// only meaningful when Body.Step == "complete".
type LoginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      LoginBody
}

// LoginTotpInput is the body for POST /auth/login/totp. Exactly one of
// Code (6-digit authenticator) or RecoveryCode must be supplied.
type LoginTotpInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		ChallengeToken string `json:"challengeToken" minLength:"1"`
		Code           string `json:"code,omitempty" pattern:"^$|^[0-9]{6}$"`
		RecoveryCode   string `json:"recoveryCode,omitempty" pattern:"^$|^[A-Za-z0-9-]{10,20}$"`
	}
}

// LoginTotpOutput is the response for POST /auth/login/totp.
type LoginTotpOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// RefreshInput is the request for POST /auth/refresh. The refresh token
// is read from the nd_rt httpOnly cookie; there is no request body.
type RefreshInput struct {
	UserAgent     string      `header:"User-Agent"`
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

// RefreshOutput is the response for POST /auth/refresh.
type RefreshOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// LogoutInput is the request for POST /auth/logout. The refresh token is
// read from the nd_rt httpOnly cookie.
type LogoutInput struct {
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

// LogoutOutput is the response for POST /auth/logout.
type LogoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Ok bool `json:"ok"`
	}
}

// OIDCStartOutput is the response for GET /auth/oidc/google/start.
// The nonce is embedded inside the signed state JWT and is not returned
// separately to avoid leaking it to the client.
type OIDCStartOutput struct {
	Body struct {
		AuthorizationURL string `json:"authorizationUrl"`
		State            string `json:"state"`
	}
}

// OIDCCallbackInput is the query for GET /auth/oidc/{provider}/callback.
//
// Identity providers redirect back to this endpoint with either an
// authorization `code` on success, or with `error` / `error_description`
// query parameters when the user denied consent or the relying-party
// configuration was rejected at the IdP. The callback handlers check
// `Error` first so a provider-side rejection is surfaced as
// AUTH.OIDC.PROVIDER_REJECTED instead of leaking through as the
// downstream "id_token invalid" code.
type OIDCCallbackInput struct {
	UserAgent string `header:"User-Agent"`
	Code      string `query:"code"`
	State     string `query:"state"`
	// Error is the OAuth2 / OIDC error slug supplied by the IdP when it
	// declines the sign-in (e.g. "access_denied", "invalid_scope"). Empty
	// on the happy path.
	Error string `query:"error"`
	// ErrorDescription is the optional human-readable explanation the
	// IdP attached to Error. May contain provider-controlled text, so
	// the handlers forward it through ProblemDetails extensions but
	// never log it verbatim.
	ErrorDescription string `query:"error_description"`
}

// OIDCCallbackOutput is the response for OIDC callback. It is a
// discriminated envelope (LoginBody): when step="complete" the tokens
// are populated and the refresh cookie is set; when step="totp_required"
// only challengeToken is populated and the client must finish at
// POST /auth/login/totp. This mirrors the password and magic-link login
// paths so an account that enrolled app-level TOTP is challenged on
// every login surface.
type OIDCCallbackOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      LoginBody
}

// MeBody is the public DTO for the authenticated user profile, shared
// by GET /me and PATCH /me.
type MeBody struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
	// Timezone is the user-level IANA timezone preference. May be "UTC"
	// when the user has not customized it; the client should fall back to
	// the workspace timezone for presentation if desired.
	Timezone string `json:"timezone"`
	// Country is the user-level ISO 3166-1 alpha-2 country, or empty
	// string when unset.
	Country string `json:"country"`
	// WeekStart is the user's preferred first day of the week for calendar grids.
	// One of "mon" (default), "sun", "sat".
	WeekStart       string `json:"weekStart" enum:"mon,sun,sat"`
	ThemePreference string `json:"themePreference" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,glass-light,glass-dark,system"`
	// CalendarShiftDefault controls how shifting a calendar event behaves
	// by default when the event has linked tasks. "ask" prompts the user
	// each time, "sync_always" shifts every linked task by the same delta,
	// "task_only_always" shifts only the event and leaves the linked tasks
	// in place.
	CalendarShiftDefault string  `json:"calendarShiftDefault" enum:"ask,sync_always,task_only_always" doc:"Controls how shifting a calendar event behaves by default when the event has linked tasks. \"ask\" prompts the user each time, \"sync_always\" shifts every linked task by the same delta, \"task_only_always\" shifts only the event and leaves the linked tasks in place."`
	AvatarURL            *string `json:"avatarUrl,omitempty"`

	// Notification channel toggles. Exposed on /me so the settings UI
	// can render them without a separate request; mutated via PATCH /me.
	NotifEmailDigest     bool `json:"notifEmailDigest"`
	NotifEmailMention    bool `json:"notifEmailMention"`
	NotifEmailAssignment bool `json:"notifEmailAssignment"`
	NotifEmailDueSoon    bool `json:"notifEmailDueSoon"`
	NotifWebPush         bool `json:"notifWebPush"`

	// IsInstanceAdmin is true when the user has an active instance admin grant.
	IsInstanceAdmin bool `json:"isInstanceAdmin"`
}

// MeOutput is the response for GET /me.
type MeOutput struct {
	Body MeBody
}

// PatchMeInput is the body for PATCH /me. All fields are optional;
// only non-nil fields are applied.
type PatchMeInput struct {
	Body PatchMeInputBody
}

// PatchMeInputBody carries the optional fields for PATCH /me.
type PatchMeInputBody struct {
	DisplayName *string `json:"displayName,omitempty" minLength:"1" maxLength:"100"`
	Locale      *string `json:"locale,omitempty" maxLength:"10"`
	// Timezone is an optional IANA timezone. Validated via time.LoadLocation;
	// invalid values return AUTH.VALIDATION.
	Timezone *string `json:"timezone,omitempty" maxLength:"64"`
	// Country is an optional ISO 3166-1 alpha-2 code. Empty string clears it.
	Country *string `json:"country,omitempty" pattern:"^$|^[A-Z]{2}$"`
	// WeekStart is the user's preferred first day of the week for calendar grids.
	// One of "mon", "sun", "sat".
	WeekStart       *string `json:"weekStart,omitempty" enum:"mon,sun,sat"`
	ThemePreference *string `json:"themePreference,omitempty" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,glass-light,glass-dark,system"`
	// CalendarShiftDefault controls how shifting a calendar event behaves
	// by default when the event has linked tasks. "ask" prompts the user
	// each time, "sync_always" shifts every linked task by the same delta,
	// "task_only_always" shifts only the event and leaves the linked tasks
	// in place.
	CalendarShiftDefault *string `json:"calendarShiftDefault,omitempty" enum:"ask,sync_always,task_only_always" doc:"Controls how shifting a calendar event behaves by default when the event has linked tasks. \"ask\" prompts the user each time, \"sync_always\" shifts every linked task by the same delta, \"task_only_always\" shifts only the event and leaves the linked tasks in place."`
	AvatarURL            *string `json:"avatarUrl,omitempty" maxLength:"1024"`

	// Notification channel toggles. nil leaves the column untouched.
	NotifEmailDigest     *bool `json:"notifEmailDigest,omitempty"`
	NotifEmailMention    *bool `json:"notifEmailMention,omitempty"`
	NotifEmailAssignment *bool `json:"notifEmailAssignment,omitempty"`
	NotifEmailDueSoon    *bool `json:"notifEmailDueSoon,omitempty"`
	NotifWebPush         *bool `json:"notifWebPush,omitempty"`
}

// PatchMeOutput is the response for PATCH /me. It returns the updated
// profile in the same shape as GET /me.
type PatchMeOutput struct {
	Body MeBody
}

// SessionSummary is the public DTO for a refresh-token session, used
// by the /settings/security sessions panel. It intentionally omits
// the refresh hash and internal id.
type SessionSummary struct {
	ID         string `json:"id" doc:"Session public id (UUID v7)"`
	UserAgent  string `json:"userAgent"`
	IPAddress  string `json:"ipAddress"`
	Current    bool   `json:"current" doc:"True if this matches the refresh cookie on the current request"`
	CreatedAt  int64  `json:"createdAt" doc:"Session creation time, unix seconds"`
	LastUsedAt *int64 `json:"lastUsedAt,omitempty" doc:"Last activity time, unix seconds"`
	ExpiresAt  int64  `json:"expiresAt" doc:"Expiry time, unix seconds"`
}

// ListSessionsInput binds the refresh cookie so the handler can mark
// the current session in the response.
type ListSessionsInput struct {
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

// ListSessionsOutput is the response for GET /me/sessions.
type ListSessionsOutput struct {
	Body struct {
		Items []SessionSummary `json:"items"`
	}
}

// RevokeSessionInput is the request for DELETE /me/sessions/{sessionId}.
type RevokeSessionInput struct {
	SessionID string `path:"sessionId" doc:"Session public id (UUID v7)"`
}

// RevokeSessionOutput is the response for DELETE /me/sessions/{sessionId}.
type RevokeSessionOutput struct {
	Body MeRevokeSessionOutputBody
}

// MeRevokeSessionOutputBody is the response body for DELETE /me/sessions/{sessionId}.
type MeRevokeSessionOutputBody struct {
	Ok bool `json:"ok"`
}

// RevokeAllOtherSessionsInput binds the refresh cookie so the handler
// can preserve the session on the current request while revoking the
// rest.
type RevokeAllOtherSessionsInput struct {
	RefreshCookie http.Cookie `cookie:"nd_rt"`
}

// RevokeAllOtherSessionsOutput is the response for DELETE /me/sessions.
type RevokeAllOtherSessionsOutput struct {
	Body struct {
		Ok      bool `json:"ok"`
		Revoked int  `json:"revoked" doc:"Number of sessions revoked"`
	}
}

// ChangePasswordInput is the body for POST /me/password. Both fields
// are required; the handler verifies currentPassword against the
// stored Argon2id hash before accepting newPassword.
type ChangePasswordInput struct {
	RefreshCookie http.Cookie `cookie:"nd_rt"`
	Body          struct {
		CurrentPassword string `json:"currentPassword" minLength:"1" maxLength:"256"`
		NewPassword     string `json:"newPassword" minLength:"8" maxLength:"256"`
	}
}

// TotpStatusOutput is the response for GET /me/totp.
type TotpStatusOutput struct {
	Body struct {
		Status string `json:"status" enum:"disabled,pending,enabled" doc:"disabled = no secret, pending = secret issued but never confirmed, enabled = confirmed"`
	}
}

// TotpEnrollInput is the body for POST /me/totp/enroll. Starting a
// pending TOTP secret requires password reverification so a stolen
// bearer token cannot enroll a second factor.
type TotpEnrollInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"256"`
	}
}

// TotpEnrollOutput is the response for POST /me/totp/enroll. The
// server returns the otpauth:// URL (for QR rendering on the client)
// plus the raw base32 secret so the user can type it in manually.
type TotpEnrollOutput struct {
	Body struct {
		OtpauthURL string `json:"otpauthUrl"`
		Secret     string `json:"secret" doc:"Base32-encoded secret, for manual entry"`
	}
}

// TotpConfirmInput is the body for POST /me/totp/confirm.
type TotpConfirmInput struct {
	Body struct {
		Code     string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
		Password string `json:"password" minLength:"1" maxLength:"256"`
	}
}

// TotpConfirmOutput is the response for POST /me/totp/confirm. The
// recovery codes are returned in plaintext exactly once at this point;
// the server stores only their SHA-256 hashes.
type TotpConfirmOutput struct {
	Body struct {
		Ok            bool     `json:"ok"`
		RecoveryCodes []string `json:"recoveryCodes"`
	}
}

// TotpRegenerateRecoveryCodesInput is the body for POST /me/totp/recovery-codes.
type TotpRegenerateRecoveryCodesInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"256"`
	}
}

// TotpRegenerateRecoveryCodesOutput is the response for POST /me/totp/recovery-codes.
type TotpRegenerateRecoveryCodesOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
}

// TotpRecoveryCodesStatusOutput is the response for GET /me/totp/recovery-codes.
type TotpRecoveryCodesStatusOutput struct {
	Body struct {
		Remaining int `json:"remaining"`
	}
}

// TotpDisableInput is the body for DELETE /me/totp. Requires the
// current password to guard against session-hijack scenarios.
type TotpDisableInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" maxLength:"256"`
	}
}

// TotpDisableOutput is the response for DELETE /me/totp.
type TotpDisableOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ChangePasswordOutput is the response for POST /me/password. The
// change revokes every other session as a side effect; the count is
// returned so the UI can tell the user "you were signed out of N
// other devices".
type ChangePasswordOutput struct {
	Body struct {
		Ok                   bool `json:"ok"`
		OtherSessionsRevoked int  `json:"otherSessionsRevoked"`
	}
}

// MagicLinkRequestInput is the body for POST /auth/magic-link/request.
type MagicLinkRequestInput struct {
	Body struct {
		Email string `json:"email" format:"email" maxLength:"254"`
	}
}

// MagicLinkRequestOutput is the response for POST /auth/magic-link/request.
// It always returns ok=true regardless of whether the email exists, to
// prevent email enumeration.
type MagicLinkRequestOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// MagicLinkVerifyInput is the query for GET /auth/magic-link/verify.
type MagicLinkVerifyInput struct {
	UserAgent string `header:"User-Agent"`
	Token     string `query:"token"`
}

// MagicLinkVerifyOutput is the response for GET /auth/magic-link/verify.
// Like the password login it is a discriminated envelope: a 2FA-enrolled
// account yields step="totp_required" with only a challengeToken and no
// refresh cookie, forcing the client to finish at POST /auth/login/totp;
// a single-factor account yields step="complete" with the access token
// and the refresh cookie set.
type MagicLinkVerifyOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      LoginBody
}
