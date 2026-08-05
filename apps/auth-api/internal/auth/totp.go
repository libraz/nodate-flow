package auth

import (
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// GenerateTotpSecret returns a fresh 20-byte random secret suitable
// for encoding into an otpauth URL.
func GenerateTotpSecret() ([]byte, error) { return authn.GenerateTotpSecret() }

// TotpOtpauthURL builds an otpauth://totp/ URL from a raw secret.
func TotpOtpauthURL(issuer, accountName string, secret []byte) string {
	return authn.TotpOtpauthURL(issuer, accountName, secret)
}

// VerifyTotp constant-time-compares the submitted code against the
// codes valid at now +/- 1 window. Returns true on a match.
func VerifyTotp(secret []byte, code string, now time.Time) bool {
	return authn.VerifyTotp(secret, code, now)
}

// VerifyTotpStep constant-time-compares the submitted code against the
// codes valid at now +/- 1 window and returns the matched TOTP
// time-step so the caller can enforce RFC 6238 5.2 one-time-use.
func VerifyTotpStep(secret []byte, code string, now time.Time) (step int64, ok bool) {
	return authn.VerifyTotpStep(secret, code, now)
}

// TotpCode returns the TOTP code valid for the secret at the given
// instant. Mirrors what an authenticator app would display.
func TotpCode(secret []byte, t time.Time) string {
	return authn.TotpCode(secret, t)
}
