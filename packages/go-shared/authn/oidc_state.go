package authn

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// oidcStateAudience is a dedicated audience for the short-lived OIDC
// state token. Using a distinct audience prevents any cross-use with
// access tokens or TOTP challenges.
const oidcStateAudience = "oidc-state"

// OIDCStateTTL is how long the OIDC flow has to complete after the
// authorization URL is generated.
const OIDCStateTTL = 10 * time.Minute

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
	exp := now.Add(OIDCStateTTL)
	claims := OIDCStateClaims{
		Nonce: nonce,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{oidcStateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(j.priv)
	if err != nil {
		return "", fmt.Errorf("authn: sign oidc state: %w", err)
	}
	return signed, nil
}

// VerifyOIDCState parses and validates a signed OIDC state token.
// Returns the embedded nonce on success.
func (j *JWTIssuer) VerifyOIDCState(token string) (string, error) {
	parsed, err := jwt.ParseWithClaims(token, &OIDCStateClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("authn: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(oidcStateAudience))
	if err != nil {
		return "", err
	}
	claims, ok := parsed.Claims.(*OIDCStateClaims)
	if !ok || !parsed.Valid || claims.Nonce == "" {
		return "", errors.New("authn: invalid oidc state claims")
	}
	return claims.Nonce, nil
}
