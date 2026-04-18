// Package auth contains password hashing, JWT, opaque token, and OIDC
// helpers used by the HTTP authentication pipeline. The package is
// transport-agnostic; HTTP wiring lives in apps/flow-api/internal/http/handlers/auth
// and apps/flow-api/internal/http/middleware.
package auth

import "github.com/nodate-flow/nodate-flow/packages/go-shared/authn"

// ErrInvalidPasswordHash is returned when an encoded hash cannot be parsed.
var ErrInvalidPasswordHash = authn.ErrInvalidPasswordHash

// DummyHash returns a pre-computed argon2id hash that can be verified
// against any password to equalise timing for non-existent users. The
// comparison will always fail, but the time spent is indistinguishable
// from a real verification.
func DummyHash() string { return authn.DummyHash() }

// HashPassword hashes the given plaintext password with argon2id and returns
// the canonical encoded form ($argon2id$v=19$...).
func HashPassword(password string) (string, error) { return authn.HashPassword(password) }

// VerifyPassword reports whether the plaintext password matches the
// encoded argon2id hash. It is constant-time on the comparison step.
func VerifyPassword(encoded, password string) (bool, error) {
	return authn.VerifyPassword(encoded, password)
}
