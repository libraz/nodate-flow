package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
)

// JWTIssuer signs and verifies short-lived access tokens (EdDSA).
// It is intentionally compatible with flow-api's JWTIssuer so that
// tokens issued by either service are accepted by the other.
type JWTIssuer struct {
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	issuer   string
	audience string
	ttl      time.Duration
}

// AccessClaims is the structured claims body of an access token.
type AccessClaims struct {
	UserPublicID    types.PublicID `json:"sub_uid"`
	SessionPublicID types.PublicID `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// NewJWTIssuer constructs a JWTIssuer. If priv is nil an ephemeral key is
// generated; this fallback is for development only.
func NewJWTIssuer(priv ed25519.PrivateKey, issuer, audience string, ttl time.Duration) (*JWTIssuer, error) {
	if priv == nil {
		pub, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("auth: generate ephemeral key: %w", err)
		}
		slog.Warn("auth: using ephemeral JWT signing key; tokens will not survive restart")
		return &JWTIssuer{priv: generated, pub: pub, issuer: issuer, audience: audience, ttl: ttl}, nil
	}
	return &JWTIssuer{priv: priv, pub: priv.Public().(ed25519.PublicKey), issuer: issuer, audience: audience, ttl: ttl}, nil
}

// Sign issues a signed access token for the given user + session public id.
func (j *JWTIssuer) Sign(userPublicID types.PublicID, sessionPublicID types.PublicID) (string, time.Time, error) {
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
		return "", time.Time{}, fmt.Errorf("auth: sign jwt: %w", err)
	}
	return signed, exp, nil
}

// Verify parses and validates an access token, returning its claims.
func (j *JWTIssuer) Verify(token string) (*AccessClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, &AccessClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("auth: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(j.audience))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*AccessClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("auth: invalid jwt claims")
	}
	return claims, nil
}
