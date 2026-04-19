package auth

import (
	"crypto/ed25519"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// JWTIssuer signs and verifies short-lived access tokens (EdDSA) and
// provides extensions (TOTP step-up, OIDC state) via the embedded
// [authn.JWTIssuer].
type JWTIssuer struct {
	*authn.JWTIssuer
}

// AccessClaims is the structured claims body of an access token.
type AccessClaims = authn.AccessClaims

// TotpChallengeClaims is the claim body of a TOTP step-up token.
type TotpChallengeClaims = authn.TotpChallengeClaims

// OIDCStateClaims is the claim body of a signed OIDC state token.
type OIDCStateClaims = authn.OIDCStateClaims

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
func (j *JWTIssuer) Sign(userPublicID dbtype.PublicID, sessionPublicID dbtype.PublicID) (string, time.Time, error) {
	return j.JWTIssuer.Sign(userPublicID, sessionPublicID)
}

// Verify parses and validates an access token, returning its claims.
func (j *JWTIssuer) Verify(token string) (*AccessClaims, error) {
	return j.JWTIssuer.Verify(token)
}
