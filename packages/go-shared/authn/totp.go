package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 mandates HMAC-SHA1 for TOTP compatibility.
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"net/url"
	"time"
)

// TOTP parameters. All three are fixed at the RFC 6238 defaults so the
// generated otpauth:// URL stays compatible with Google Authenticator,
// 1Password, Authy, etc. without carrying explicit overrides.
const (
	totpDigits    = 6
	totpPeriod    = 30
	totpSkew      = 1 // accept +/-1 window (+/-30s clock drift)
	totpSecretLen = 20
)

// GenerateTotpSecret returns a fresh 20-byte random secret suitable
// for encoding into an otpauth URL.
func GenerateTotpSecret() ([]byte, error) {
	buf := make([]byte, totpSecretLen)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("authn: read totp secret: %w", err)
	}
	return buf, nil
}

// TotpOtpauthURL builds an otpauth://totp/ URL from a raw secret.
// Issuer and accountName become the label and issuer query parameter
// so authenticator apps group entries by issuer.
func TotpOtpauthURL(issuer, accountName string, secret []byte) string {
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	label := url.PathEscape(issuer) + ":" + url.PathEscape(accountName)
	q := url.Values{}
	q.Set("secret", b32)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpAt computes the HOTP/TOTP 6-digit code at the given unix time.
func totpAt(secret []byte, t time.Time) string {
	counter := uint64(t.Unix() / totpPeriod)
	buf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		buf[i] = byte(counter & 0xff)
		counter >>= 8
	}
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, bin%mod)
}

// VerifyTotp constant-time-compares the submitted code against the
// codes valid at now+/-totpSkew windows. Returns true on a match.
func VerifyTotp(secret []byte, code string, now time.Time) bool {
	if len(code) != totpDigits {
		return false
	}
	for i := -totpSkew; i <= totpSkew; i++ {
		candidate := totpAt(secret, now.Add(time.Duration(i)*totpPeriod*time.Second))
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}
