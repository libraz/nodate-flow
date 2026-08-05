package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// oidcProvisionParams carries the verified claims a single OIDC callback
// has already extracted, in a provider-agnostic shape, so the shared
// find-link-or-provision logic lives in one place instead of being
// copy-pasted across the Google / GitHub / Microsoft callbacks.
type oidcProvisionParams struct {
	// Provider is the identities.provider enum value ("google" /
	// "github" / "microsoft").
	Provider generated.IdentitiesProvider
	// Subject is the provider-stable subject identifier (sub / GitHub
	// numeric id) bound to the identity row.
	Subject string
	// Email is the IdP-verified email address. Callers MUST have already
	// rejected unverified emails before reaching this helper.
	Email string
	// DisplayName seeds users.display_name when a brand-new user is
	// provisioned. Ignored when an existing user is linked.
	DisplayName string
	// Locale seeds users.locale on first provisioning. Defaults to "en"
	// when empty.
	Locale string
	// AllowEmailLink controls whether this provider may bind a new
	// identity onto an existing same-email account. Microsoft disables
	// this because email_verified alone is not enough cross-tenant
	// account-linking proof.
	AllowEmailLink bool
}

// resolveOIDCUser maps a verified OIDC sign-in to an internal user,
// returning the internal id and public UUID for token issuance.
//
// Resolution order:
//  1. An existing identity for (provider, subject) -> that user logs in.
//  2. No identity, but a user already holds the verified email -> create
//     an identity row linking this provider to the existing account
//     (account linking). This is login, not registration, so it is NOT
//     gated by RegistrationOpen; it is what lets a local-password user
//     adopt SSO without tripping the uniq_users_email constraint (the
//     defect this helper fixes, which previously surfaced as a 500).
//  3. No identity and no user -> provision a fresh user + identity,
//     gated by RegistrationOpen.
//
// Linking in step 2 is safe because the caller proved control of the
// email via the IdP's email_verified claim; binding a new provider to the
// matching account cannot hijack an address the signer does not own.
func (d Deps) resolveOIDCUser(ctx context.Context, p oidcProvisionParams) (uint32, types.PublicID, error) {
	ident, err := d.Queries.FindIdentityByProviderSubject(ctx, generated.FindIdentityByProviderSubjectParams{
		Provider: p.Provider,
		Subject:  p.Subject,
	})
	switch {
	case err == nil:
		// Existing identity: this provider/subject already maps to a user.
		pub, qerr := d.Queries.FindUserPublicIdById(ctx, ident.UserID)
		if qerr != nil {
			return 0, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
		}
		return ident.UserID, pub, nil
	case errors.Is(err, sql.ErrNoRows):
		// Fall through to link-or-provision below.
	default:
		return 0, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
	}

	if p.AllowEmailLink {
		// No identity for (provider, subject). Try to link onto an
		// existing account that already holds this verified email.
		existing, ferr := d.Queries.FindUserByEmail(ctx, p.Email)
		switch {
		case ferr == nil:
			// Account linking: bind this provider to the existing user.
			if cerr := d.createOIDCIdentity(ctx, existing.ID, p.Provider, p.Subject); cerr != nil {
				return 0, types.PublicID{}, cerr
			}
			return existing.ID, existing.PublicID, nil
		case errors.Is(ferr, sql.ErrNoRows):
			// No user holds this email: fall through to fresh provisioning.
		default:
			return 0, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
		}
	} else {
		_, ferr := d.Queries.FindUserByEmail(ctx, p.Email)
		switch {
		case ferr == nil:
			return 0, types.PublicID{}, httpErr(apierrors.AuthOidcIdTokenInvalid)
		case errors.Is(ferr, sql.ErrNoRows):
			// No user holds this email: fall through to fresh provisioning.
		default:
			return 0, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
		}
	}

	// Brand-new email: provision a new user, gated by RegistrationOpen.
	if !d.RegistrationOpen {
		return 0, types.PublicID{}, httpErr(apierrors.AuthRegisterInstanceRegistrationDisabled)
	}
	userPub := types.New()
	locale := p.Locale
	if locale == "" {
		locale = "en"
	}
	uid, rerr := d.Queries.RegisterUser(ctx, generated.RegisterUserParams{
		PublicID:        userPub,
		Email:           p.Email,
		DisplayName:     p.DisplayName,
		Locale:          locale,
		ThemePreference: generated.UsersThemePreference("system"),
	})
	if rerr != nil {
		return 0, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
	}
	userID := uint32(uid) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED AUTO_INCREMENT) fits uint32 in any realistic deployment
	if cerr := d.createOIDCIdentity(ctx, userID, p.Provider, p.Subject); cerr != nil {
		return 0, types.PublicID{}, cerr
	}
	return userID, userPub, nil
}

// finishOIDCLogin is the shared post-resolution path for all three OIDC
// callbacks. Given the user resolved by resolveOIDCUser, it either
// returns a totp_required step-up challenge (when the account has
// confirmed app-level TOTP) or issues session tokens directly.
//
// Step-up rationale: a user who explicitly enrolled app TOTP
// (MfaConfirmedAt set on their local identity) must satisfy it on every
// login path, mirroring the password and magic-link paths. We do NOT
// silently trust the IdP's own MFA in place of the app TOTP the user
// opted into. The client must finish at POST /auth/login/totp.
//
// A user with no local identity (OIDC-only account) or an unconfirmed
// secret has no app TOTP gate and completes login directly.
func (d Deps) finishOIDCLogin(ctx context.Context, userID uint32, userPub types.PublicID, userAgent string) (*OIDCCallbackOutput, error) {
	ident, ierr := d.Queries.FindLocalIdentityByUserId(ctx, userID)
	switch {
	case ierr == nil:
		if ident.MfaConfirmedAt.Valid && len(ident.MfaSecretCiphertext.String) > 0 {
			challenge, _, cerr := d.JWT.SignTotpChallenge(userPub.String())
			if cerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &OIDCCallbackOutput{
				Status: http.StatusFound,
				Location: oidcCompleteURL(d.AccountsWebURL, url.Values{
					"step":           []string{"totp_required"},
					"challengeToken": []string{challenge},
				}),
				SetCookie: []http.Cookie{clearedOIDCStateCookie(d.CookieSecure)},
				Body: LoginBody{
					Step:           "totp_required",
					ChallengeToken: challenge,
				},
			}, nil
		}
	case errors.Is(ierr, sql.ErrNoRows):
		// No local identity: OIDC-only account, no app TOTP gate applies.
	default:
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	tokens, refresh, err := IssueTokens(ctx, d, userID, userPub, userAgent, authn.ClientIPFromContext(ctx))
	if err != nil {
		return nil, err
	}
	return &OIDCCallbackOutput{
		Status:   http.StatusFound,
		Location: oidcCompleteURL(d.AccountsWebURL, url.Values{"step": []string{"complete"}}),
		// The state verifier has served its purpose; evicting it keeps a
		// stale cookie from lingering in the browser for the rest of the
		// state's lifetime.
		SetCookie: []http.Cookie{
			clearedOIDCStateCookie(d.CookieSecure),
			newRefreshCookie(refresh, d.CookieSecure),
		},
		Body: LoginBody{
			Step:        "complete",
			AccessToken: tokens.AccessToken,
			ExpiresAt:   tokens.ExpiresAt,
			UserID:      tokens.UserID,
		},
	}, nil
}

func oidcCompleteURL(accountsWebURL string, fragment url.Values) string {
	base := strings.TrimRight(accountsWebURL, "/")
	if base == "" {
		base = "/"
	}
	target := base + "/oidc/complete"
	if encoded := fragment.Encode(); encoded != "" {
		target += "#" + encoded
	}
	return target
}

// createOIDCIdentity inserts an identities row binding an OIDC
// provider/subject to an existing user, allocating a fresh public_id.
func (d Deps) createOIDCIdentity(ctx context.Context, userID uint32, provider generated.IdentitiesProvider, subject string) error {
	if _, err := d.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
		PublicID: types.New(),
		UserID:   userID,
		Provider: provider,
		Subject:  subject,
	}); err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	return nil
}
