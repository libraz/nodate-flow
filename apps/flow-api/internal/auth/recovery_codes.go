// Recovery codes for TOTP. Generated as 12 chars from a Crockford-style
// base32 alphabet, grouped XXXX-XXXX-XXXX. Stored as SHA-256 hashes;
// the plaintext is shown to the user only once.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
)

// recoveryAlphabet is Crockford base32 (no I, L, O, U) — 32 chars.
const recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// recoveryCodesPerUser is the number of codes issued per (re)generation.
const recoveryCodesPerUser = 10

// GenerateRecoveryCodes returns 10 fresh recovery codes (plaintext) plus
// their SHA-256 hashes, in matching order.
func GenerateRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, recoveryCodesPerUser)
	hashes := make([][]byte, recoveryCodesPerUser)
	for i := 0; i < recoveryCodesPerUser; i++ {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, fmt.Errorf("auth: read recovery code: %w", err)
		}
		var sb strings.Builder
		sb.Grow(14)
		for j := 0; j < 12; j++ {
			if j == 4 || j == 8 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(raw[j])%len(recoveryAlphabet)])
		}
		code := sb.String()
		codes[i] = code
		hashes[i] = HashRecoveryCode(code)
	}
	return codes, hashes, nil
}

// HashRecoveryCode normalizes (uppercase, strip dashes/spaces) then
// SHA-256s the result. Returns the 32-byte digest.
func HashRecoveryCode(code string) []byte {
	norm := strings.ToUpper(code)
	norm = strings.ReplaceAll(norm, "-", "")
	norm = strings.ReplaceAll(norm, " ", "")
	sum := sha256.Sum256([]byte(norm))
	return sum[:]
}
