package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// JWTIssuer signs and verifies short-lived access tokens (EdDSA).
type JWTIssuer struct {
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	issuer   string
	audience string
	ttl      time.Duration
}

// AccessClaims is the structured claims body of an access token.
type AccessClaims struct {
	UserPublicID    dbtype.PublicID `json:"sub_uid"`
	SessionPublicID dbtype.PublicID `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// Priv returns the private key so app-specific extensions (TOTP, OIDC)
// that embed or wrap JWTIssuer can sign custom tokens.
func (j *JWTIssuer) Priv() ed25519.PrivateKey { return j.priv }

// Pub returns the public key for custom verification use cases.
func (j *JWTIssuer) Pub() ed25519.PublicKey { return j.pub }

// Issuer returns the configured issuer string.
func (j *JWTIssuer) Issuer() string { return j.issuer }

// Audience returns the configured audience string.
func (j *JWTIssuer) Audience() string { return j.audience }

// TTL returns the configured access token time-to-live.
func (j *JWTIssuer) TTL() time.Duration { return j.ttl }

// NewJWTIssuer constructs a JWTIssuer. If priv is nil an ephemeral key is
// generated and slog.Warn is emitted; this fallback is for development only.
func NewJWTIssuer(priv ed25519.PrivateKey, issuer, audience string, ttl time.Duration) (*JWTIssuer, error) {
	if priv == nil {
		pub, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("authn: generate ephemeral key: %w", err)
		}
		slog.Warn("authn: using ephemeral JWT signing key; tokens will not survive restart")
		return &JWTIssuer{priv: generated, pub: pub, issuer: issuer, audience: audience, ttl: ttl}, nil
	}
	return &JWTIssuer{priv: priv, pub: priv.Public().(ed25519.PublicKey), issuer: issuer, audience: audience, ttl: ttl}, nil
}

// Sign issues a signed access token for the given user + session public id.
// The session public id is embedded as the "sid" claim so downstream
// middleware can identify the calling session without requiring the
// refresh cookie.
func (j *JWTIssuer) Sign(userPublicID dbtype.PublicID, sessionPublicID dbtype.PublicID) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(j.ttl)
	claims := AccessClaims{
		UserPublicID:    userPublicID,
		SessionPublicID: sessionPublicID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{j.audience},
			Subject:   userPublicID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(j.priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authn: sign jwt: %w", err)
	}
	return signed, exp, nil
}

// Verify parses and validates an access token, returning its claims.
func (j *JWTIssuer) Verify(token string) (*AccessClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, &AccessClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("authn: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(j.audience))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*AccessClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("authn: invalid jwt claims")
	}
	return claims, nil
}
