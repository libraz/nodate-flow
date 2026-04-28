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
	// Provider binds the state token to the OIDC provider that minted
	// it ("google" / "github" / "microsoft"). Verified on callback so a
	// state issued for one provider's redirect URI cannot be replayed
	// against another's.
	Provider string `json:"provider,omitempty"`
	jwt.RegisteredClaims
}

// SignOIDCState issues a signed, short-lived state parameter for the
// OIDC authorization flow. The returned token encodes the nonce so the
// callback can verify both CSRF (state signature) and token replay
// (nonce in id_token) in a single round-trip.
//
// Deprecated: prefer [JWTIssuer.SignOIDCStateForProvider] which also
// binds the provider so a state issued for one redirect URI cannot be
// replayed against another. Kept for backward compatibility with tests.
func (j *JWTIssuer) SignOIDCState(nonce string) (string, error) {
	return j.SignOIDCStateForProvider(nonce, "")
}

// SignOIDCStateForProvider issues a signed, short-lived state parameter
// bound to a specific OIDC provider. The provider claim is verified on
// callback to prevent cross-provider state replay (a state minted for
// /oidc/google/callback cannot be redeemed at /oidc/github/callback).
func (j *JWTIssuer) SignOIDCStateForProvider(nonce, provider string) (string, error) {
	now := time.Now().UTC()
	exp := now.Add(OIDCStateTTL)
	claims := OIDCStateClaims{
		Nonce:    nonce,
		Provider: provider,
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
//
// Deprecated: prefer [JWTIssuer.VerifyOIDCStateForProvider], which also
// verifies the provider binding. This shim exists for callers that do
// not need the provider check.
func (j *JWTIssuer) VerifyOIDCState(token string) (string, error) {
	claims, err := j.parseOIDCState(token)
	if err != nil {
		return "", err
	}
	return claims.Nonce, nil
}

// VerifyOIDCStateForProvider parses and validates a signed OIDC state
// token, additionally requiring the provider claim to match the caller-
// supplied expected value. Returns the embedded nonce on success. An
// empty provider claim is rejected so legacy unbound tokens are no
// longer accepted at provider-aware callbacks.
func (j *JWTIssuer) VerifyOIDCStateForProvider(token, expectedProvider string) (string, error) {
	claims, err := j.parseOIDCState(token)
	if err != nil {
		return "", err
	}
	if claims.Provider == "" || claims.Provider != expectedProvider {
		return "", errors.New("authn: oidc state provider mismatch")
	}
	return claims.Nonce, nil
}

func (j *JWTIssuer) parseOIDCState(token string) (*OIDCStateClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, &OIDCStateClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("authn: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(oidcStateAudience))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*OIDCStateClaims)
	if !ok || !parsed.Valid || claims.Nonce == "" {
		return nil, errors.New("authn: invalid oidc state claims")
	}
	return claims, nil
}
