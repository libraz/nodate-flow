package authn

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
	// CookieHash binds the state to the browser that started the flow:
	// it is the SHA-256 of a verifier handed to that browser as an
	// HttpOnly cookie. Only the hash travels in the URL, so a state
	// recovered from a referer header or a proxy log is inert without
	// the cookie.
	CookieHash string `json:"csh,omitempty"`
	jwt.RegisteredClaims
}

// OIDCStateBinding is the pair [JWTIssuer.NewOIDCStateBinding] produces:
// the state parameter that goes into the authorization URL, and the
// verifier that must be stored in the caller's browser as a cookie.
// Redeeming the state at the callback requires both halves.
type OIDCStateBinding struct {
	// State is the signed state parameter for the authorization URL.
	State string
	// CookieValue is the verifier to hand to the browser. It never
	// appears in a URL.
	CookieValue string
	// ExpiresAt is when the state stops being valid. Callers use it as
	// the cookie lifetime and as the single-use record's TTL.
	ExpiresAt time.Time
}

// VerifiedOIDCState is what a successfully verified state yields.
type VerifiedOIDCState struct {
	// Nonce must appear in the id_token returned by the provider.
	Nonce string
	// ID is the state's jti. Pass it to a [SingleUseStore] so the state
	// cannot be redeemed twice while its signature is still valid.
	ID string
	// ExpiresAt is the state's expiry, i.e. how long a single-use
	// record for ID has to outlive it.
	ExpiresAt time.Time
}

// NewOIDCStateBinding mints a state parameter bound to both the
// provider and the browser that starts the flow. The caller must send
// the returned CookieValue to that browser as an HttpOnly cookie and
// require it back at the callback via [JWTIssuer.VerifyOIDCStateBinding].
//
// This is the constructor browser-facing sign-in flows must use. A state
// that only proves "some state was signed by this server" is not a CSRF
// defence: an attacker can mint one from their own browser and feed it,
// with their own authorization code, to a victim who is then signed in
// as the attacker (RFC 6749 section 10.12). Binding the state to a cookie
// the attacker cannot write into the victim's browser is what closes it.
func (j *JWTIssuer) NewOIDCStateBinding(nonce, provider string) (OIDCStateBinding, error) {
	verifier := RandomHex(32)
	claims := j.newOIDCStateClaims(nonce, provider)
	claims.CookieHash = HashOIDCStateVerifier(verifier)
	signed, err := j.signOIDCState(claims)
	if err != nil {
		return OIDCStateBinding{}, err
	}
	return OIDCStateBinding{
		State:       signed,
		CookieValue: verifier,
		ExpiresAt:   claims.ExpiresAt.Time,
	}, nil
}

// VerifyOIDCStateBinding validates a state parameter against the
// verifier cookie the browser presented and the provider whose callback
// is running. On success the caller must still consume the returned ID
// through a [SingleUseStore]; signature validity alone leaves the state
// replayable for its whole lifetime.
func (j *JWTIssuer) VerifyOIDCStateBinding(token, cookieValue, expectedProvider string) (VerifiedOIDCState, error) {
	claims, err := j.parseOIDCState(token)
	if err != nil {
		return VerifiedOIDCState{}, err
	}
	if claims.Provider == "" || claims.Provider != expectedProvider {
		return VerifiedOIDCState{}, errors.New("authn: oidc state provider mismatch")
	}
	// An empty CookieHash means the state predates the binding or was
	// minted by a path that skips it. Either way it cannot prove which
	// browser started the flow, so it is refused rather than downgraded.
	if claims.CookieHash == "" {
		return VerifiedOIDCState{}, errors.New("authn: oidc state is not bound to a browser")
	}
	if cookieValue == "" {
		return VerifiedOIDCState{}, errors.New("authn: oidc state cookie missing")
	}
	if subtle.ConstantTimeCompare([]byte(claims.CookieHash), []byte(HashOIDCStateVerifier(cookieValue))) != 1 {
		return VerifiedOIDCState{}, errors.New("authn: oidc state cookie mismatch")
	}
	if claims.ID == "" || claims.ExpiresAt == nil {
		return VerifiedOIDCState{}, errors.New("authn: oidc state is not single-use capable")
	}
	return VerifiedOIDCState{
		Nonce:     claims.Nonce,
		ID:        claims.ID,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// HashOIDCStateVerifier derives the value stored in the state token from
// the verifier held by the browser.
func HashOIDCStateVerifier(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return hex.EncodeToString(sum[:])
}

// SignOIDCState issues a signed, short-lived state parameter for the
// OIDC authorization flow. The returned token encodes the nonce so the
// callback can verify both CSRF (state signature) and token replay
// (nonce in id_token) in a single round-trip.
//
// Deprecated: prefer [JWTIssuer.NewOIDCStateBinding]. A state without a
// cookie binding does not identify the browser that started the flow.
func (j *JWTIssuer) SignOIDCState(nonce string) (string, error) {
	return j.SignOIDCStateForProvider(nonce, "")
}

// SignOIDCStateForProvider issues a signed, short-lived state parameter
// bound to a specific OIDC provider. The provider claim is verified on
// callback to prevent cross-provider state replay (a state minted for
// /oidc/google/callback cannot be redeemed at /oidc/github/callback).
func (j *JWTIssuer) SignOIDCStateForProvider(nonce, provider string) (string, error) {
	return j.signOIDCState(j.newOIDCStateClaims(nonce, provider))
}

// newOIDCStateClaims builds the common claim set shared by every state
// token, so the bound and unbound constructors cannot drift on issuer,
// audience, lifetime, or jti.
func (j *JWTIssuer) newOIDCStateClaims(nonce, provider string) OIDCStateClaims {
	now := time.Now().UTC()
	return OIDCStateClaims{
		Nonce:    nonce,
		Provider: provider,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{oidcStateAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(OIDCStateTTL)),
			ID:        uuid.NewString(),
		},
	}
}

func (j *JWTIssuer) signOIDCState(claims OIDCStateClaims) (string, error) {
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
