package auth

import "github.com/nodate-flow/nodate-flow/packages/go-shared/authn"

// ErrInvalidPasswordHash is returned when an encoded hash cannot be parsed.
var ErrInvalidPasswordHash = authn.ErrInvalidPasswordHash

// DummyHash returns a pre-computed argon2id hash that can be verified
// against any password to equalise timing for non-existent users.
func DummyHash() string { return authn.DummyHash() }

// HashPassword hashes the given plaintext password with argon2id and returns
// the canonical encoded form ($argon2id$v=19$...).
func HashPassword(password string) (string, error) { return authn.HashPassword(password) }

// VerifyPassword reports whether the plaintext password matches the
// encoded argon2id hash.
func VerifyPassword(encoded, password string) (bool, error) {
	return authn.VerifyPassword(encoded, password)
}
