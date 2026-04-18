// Package auth contains password hashing, JWT, opaque token, and OIDC
// helpers used by the HTTP authentication pipeline. The package is
// transport-agnostic; HTTP wiring lives in apps/flow-api/internal/http/handlers/auth
// and apps/flow-api/internal/http/middleware.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. The encoded form is
//
//	$argon2id$v=19$m=65536,t=1,p=4$<22 b64 salt>$<43 b64 hash>
//
// which is 97 characters with a 16-byte salt and a 32-byte key. The
// identities.password_hash column is sized to hold this encoded form.
const (
	argonMemoryKiB  uint32 = 64 * 1024
	argonTime       uint32 = 1
	argonThreads    uint8  = 4
	argonSaltLength int    = 16
	argonKeyLength  uint32 = 32
)

// ErrInvalidPasswordHash is returned when an encoded hash cannot be parsed.
var ErrInvalidPasswordHash = errors.New("auth: invalid password hash encoding")

// HashPassword hashes the given plaintext password with argon2id and returns
// the canonical encoded form ($argon2id$v=19$...).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLength)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemoryKiB,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// VerifyPassword reports whether the plaintext password matches the
// encoded argon2id hash. It is constant-time on the comparison step.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// Expect: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidPasswordHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidPasswordHash
	}
	if version != argon2.Version {
		return false, ErrInvalidPasswordHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrInvalidPasswordHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidPasswordHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidPasswordHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(want, got) == 1 {
		return true, nil
	}
	return false, nil
}
