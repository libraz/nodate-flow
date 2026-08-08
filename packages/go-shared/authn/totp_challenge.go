package authn

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// totpChallengeAudience is a dedicated audience string used by the
// short-lived TOTP step-up token. Using a different audience from the
// regular access token means a stolen challenge cannot be presented
// as a bearer token on normal endpoints: [JWTIssuer.Verify] rejects
// any JWT whose audience does not equal the configured audience.
const totpChallengeAudience = "totp-challenge"

// TotpChallengeTTL is how long the login handler gives the user to
// submit the second factor after a correct password.
const TotpChallengeTTL = 5 * time.Minute

// TotpChallengeClaims is the claim body of a TOTP step-up token. The
// subject is the user's public UUID v7 string so that internal numeric
// IDs are never exposed in tokens visible to the client.
type TotpChallengeClaims struct {
	PublicID string `json:"pid"`
	jwt.RegisteredClaims
}

// SignTotpChallenge issues a short-lived step-up token after a
// successful password check for an account that has confirmed TOTP.
// The publicID parameter is the user's UUID v7 string representation.
func (j *JWTIssuer) SignTotpChallenge(publicID string) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(TotpChallengeTTL)
	claims := TotpChallengeClaims{
		PublicID: publicID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{totpChallengeAudience},
			Subject:   publicID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(j.priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authn: sign totp challenge: %w", err)
	}
	return signed, exp, nil
}

// VerifiedTotpChallenge is a step-up token that passed signature,
// issuer, audience and expiry validation. It carries the identifiers a
// caller needs to retire the token after a single redemption: the jti
// to record and how much of its lifetime is left, so the record never
// outlives the credential it guards.
type VerifiedTotpChallenge struct {
	// PublicID is the user's UUID v7 string representation.
	PublicID string
	// TokenID is the jti claim, the key under which a redemption is
	// recorded in a [SingleUseStore].
	TokenID string
	// ExpiresAt is the token's own exp claim.
	ExpiresAt time.Time
}

// RemainingTTL is how much of the challenge's lifetime is left at now.
// It is the ttl to hand [SingleUseStore.Consume] so the replay record
// is swept as soon as the token it guards would have expired anyway.
// Returns 0 once the token is past exp.
func (c VerifiedTotpChallenge) RemainingTTL(now time.Time) time.Duration {
	d := c.ExpiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// VerifyTotpChallenge parses and validates a TOTP step-up token.
//
// A valid signature is not on its own permission to complete a login:
// nothing in the token changes when it is presented, so it stays
// redeemable for its whole lifetime unless the caller records the
// returned TokenID in a [SingleUseStore] and refuses a second
// redemption. The OIDC hand-off puts the token in a URL fragment,
// where it survives in browser history long after the login finished.
func (j *JWTIssuer) VerifyTotpChallenge(token string) (VerifiedTotpChallenge, error) {
	parsed, err := jwt.ParseWithClaims(token, &TotpChallengeClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("authn: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(totpChallengeAudience))
	if err != nil {
		return VerifiedTotpChallenge{}, err
	}
	claims, ok := parsed.Claims.(*TotpChallengeClaims)
	if !ok || !parsed.Valid || claims.PublicID == "" {
		return VerifiedTotpChallenge{}, errors.New("authn: invalid totp challenge claims")
	}
	// A challenge with no jti cannot be retired after redemption, so it
	// is refused rather than silently accepted as a reusable credential.
	if claims.ID == "" {
		return VerifiedTotpChallenge{}, errors.New("authn: totp challenge missing jti")
	}
	out := VerifiedTotpChallenge{PublicID: claims.PublicID, TokenID: claims.ID}
	if claims.ExpiresAt != nil {
		out.ExpiresAt = claims.ExpiresAt.Time
	}
	return out, nil
}
