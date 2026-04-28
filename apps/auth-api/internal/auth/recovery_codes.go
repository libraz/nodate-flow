package auth

import (
	"crypto/subtle"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// GenerateRecoveryCodes returns 10 fresh recovery codes (plaintext) plus
// their SHA-256 hashes, in matching order.
func GenerateRecoveryCodes() ([]string, [][]byte, error) { return authn.GenerateRecoveryCodes() }

// HashRecoveryCode normalizes (uppercase, strip dashes/spaces) then
// SHA-256s the result. Returns the 32-byte digest.
func HashRecoveryCode(code string) []byte { return authn.HashRecoveryCode(code) }

// VerifyRecoveryCodeHash performs a defense-in-depth constant-time
// comparison between a freshly recomputed hash of the supplied code
// and the hash that was previously sent to the database WHERE clause.
//
// The SQL lookup itself filters by code_hash, so any row returned has
// already proven equality at the storage layer. This helper guards
// against a narrow but reachable class of bugs:
//
//   - mid-flight memory corruption between [HashRecoveryCode] and the
//     SQL parameter binding;
//   - a future refactor that reuses the helper for in-memory caches
//     where no DB round-trip enforces equality;
//   - a regression that swaps in a non-deterministic digest function.
//
// Using [subtle.ConstantTimeCompare] (instead of bytes.Equal) keeps
// the comparison time independent of the byte position at which the
// two hashes diverge — the same posture every other auth-side
// secret comparison takes.
//
// Returns true when the supplied code re-hashes to the expected
// digest. The caller treats false as "no match found" so the failure
// surface is identical to sql.ErrNoRows.
func VerifyRecoveryCodeHash(code string, expectedHash []byte) bool {
	recomputed := authn.HashRecoveryCode(code)
	return subtle.ConstantTimeCompare(recomputed, expectedHash) == 1
}
