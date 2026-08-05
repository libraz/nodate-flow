package auth

import "github.com/libraz/nodate-flow/packages/go-shared/authn"

// GenerateRecoveryCodes returns 10 fresh recovery codes (plaintext) plus
// their SHA-256 hashes, in matching order.
func GenerateRecoveryCodes() ([]string, [][]byte, error) { return authn.GenerateRecoveryCodes() }

// HashRecoveryCode normalizes (uppercase, strip dashes/spaces) then
// SHA-256s the result. Returns the 32-byte digest.
func HashRecoveryCode(code string) []byte { return authn.HashRecoveryCode(code) }
