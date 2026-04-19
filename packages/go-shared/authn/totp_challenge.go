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

// VerifyTotpChallenge parses and validates a TOTP step-up token.
// Returns the user's public UUID v7 string on success.
func (j *JWTIssuer) VerifyTotpChallenge(token string) (string, error) {
	parsed, err := jwt.ParseWithClaims(token, &TotpChallengeClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("authn: unexpected jwt signing method")
		}
		return j.pub, nil
	}, jwt.WithIssuer(j.issuer), jwt.WithAudience(totpChallengeAudience))
	if err != nil {
		return "", err
	}
	claims, ok := parsed.Claims.(*TotpChallengeClaims)
	if !ok || !parsed.Valid || claims.PublicID == "" {
		return "", errors.New("authn: invalid totp challenge claims")
	}
	return claims.PublicID, nil
}
