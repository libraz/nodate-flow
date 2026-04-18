package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// JWTIssuer signs and verifies short-lived access tokens (EdDSA) and
// provides flow-api-specific extensions (TOTP step-up, OIDC state).
type JWTIssuer struct {
	*authn.JWTIssuer
}

// AccessClaims is the structured claims body of an access token.
type AccessClaims = authn.AccessClaims

// NewJWTIssuer constructs a JWTIssuer. If priv is nil an ephemeral key is
// generated and slog.Warn is emitted; this fallback is for development only.
func NewJWTIssuer(priv ed25519.PrivateKey, issuer, audience string, ttl time.Duration) (*JWTIssuer, error) {
	inner, err := authn.NewJWTIssuer(priv, issuer, audience, ttl)
	if err != nil {
		return nil, err
	}
	return &JWTIssuer{JWTIssuer: inner}, nil
}

// Sign issues a signed access token for the given user + session public id.
// The session public id is embedded as the "sid" claim so downstream
// middleware can identify the calling session without requiring the
// refresh cookie (which is scoped to /auth and not sent on other paths).
func (j *JWTIssuer) Sign(userPublicID dbtype.PublicID, sessionPublicID dbtype.PublicID) (string, time.Time, error) {
	return j.JWTIssuer.Sign(userPublicID, sessionPublicID)
}

// Verify parses and validates an access token, returning its claims.
func (j *JWTIssuer) Verify(token string) (*AccessClaims, error) {
	return j.JWTIssuer.Verify(token)
}

// ---------------------------------------------------------------------------
// TOTP step-up token (flow-api only)
// ---------------------------------------------------------------------------

// totpChallengeAudience is a dedicated audience string used by the
// short-lived TOTP step-up token. Using a different audience from the
// regular access token means a stolen challenge cannot be presented
// as a bearer token on normal endpoints: [Verify] rejects any JWT
// whose audience does not equal [j.audience].
const totpChallengeAudience = "totp-challenge"

// totpChallengeTTL is how long the login handler gives the user to
// submit the second factor after a correct password.
const totpChallengeTTL = 5 * time.Minute

// TotpChallengeClaims is the claim body of a TOTP step-up token. The
// subject is the internal user id (as a string) because the TOTP
// verify handler needs the identities row, which is keyed on it.
type TotpChallengeClaims struct {
	UserID uint32 `json:"uid"`
	jwt.RegisteredClaims
}

// SignTotpChallenge issues a short-lived step-up token after a
// successful password check for an account that has confirmed TOTP.
func (j *JWTIssuer) SignTotpChallenge(userID uint32) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(totpChallengeTTL)
	claims := TotpChallengeClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.Issuer(),
			Audience:  jwt.ClaimStrings{totpChallengeAudience},
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(j.Priv())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign totp challenge: %w", err)
	}
	return signed, exp, nil
}

// VerifyTotpChallenge parses and validates a TOTP step-up token.
// Returns the internal user id on success.
func (j *JWTIssuer) VerifyTotpChallenge(token string) (uint32, error) {
	parsed, err := jwt.ParseWithClaims(token, &TotpChallengeClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("auth: unexpected jwt signing method")
		}
		return j.Pub(), nil
	}, jwt.WithIssuer(j.Issuer()), jwt.WithAudience(totpChallengeAudience))
	if err != nil {
		return 0, err
	}
	claims, ok := parsed.Claims.(*TotpChallengeClaims)
	if !ok || !parsed.Valid || claims.UserID == 0 {
		return 0, errors.New("auth: invalid totp challenge claims")
	}
	return claims.UserID, nil
}

// ---------------------------------------------------------------------------
// OIDC state token (flow-api only)
// ---------------------------------------------------------------------------

// oidcStateAudience is a dedicated audience for the short-lived OIDC
// state token. Using a distinct audience prevents any cross-use with
// access tokens or TOTP challenges.
const oidcStateAudience = "oidc-state"

// oidcStateTTL is how long the OIDC flow has to complete after the
// authorization URL is generated.
const oidcStateTTL = 10 * time.Minute

// OIDCStateClaims is the claim body of a signed OIDC state token.
type OIDCStateClaims struct {
	// Nonce is the nonce value that must appear in the id_token.
	Nonce string `json:"nonce"`
	jwt.RegisteredClaims
}

// SignOIDCState issues a signed, short-lived state parameter for the
// OIDC authorization flow. The returned token encodes the nonce so the
// callback can verify both CSRF (state signature) and token replay
// (nonce in id_token) in a single round-trip.
func (j *JWTIssuer) SignOIDCState(nonce string) (string, error) {
	now := time.Now().UTC()
	exp := now.Add(oidcStateTTL)
	claims := OIDCStateClaims{
		Nonce: nonce,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.Issuer(),
			Audience:  jwt.ClaimStrings{oidcStateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(j.Priv())
	if err != nil {
		return "", fmt.Errorf("auth: sign oidc state: %w", err)
	}
	return signed, nil
}

// VerifyOIDCState parses and validates a signed OIDC state token.
// Returns the embedded nonce on success.
func (j *JWTIssuer) VerifyOIDCState(token string) (string, error) {
	parsed, err := jwt.ParseWithClaims(token, &OIDCStateClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("auth: unexpected jwt signing method")
		}
		return j.Pub(), nil
	}, jwt.WithIssuer(j.Issuer()), jwt.WithAudience(oidcStateAudience))
	if err != nil {
		return "", err
	}
	claims, ok := parsed.Claims.(*OIDCStateClaims)
	if !ok || !parsed.Valid || claims.Nonce == "" {
		return "", errors.New("auth: invalid oidc state claims")
	}
	return claims.Nonce, nil
}
