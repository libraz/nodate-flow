// Package authn provides password hashing, JWT signing/verification, and
// opaque token generation shared by all backend services.
package authn

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
// which is 97 characters with a 16-byte salt and a 32-byte key.
const (
	argonMemoryKiB  uint32 = 64 * 1024
	argonTime       uint32 = 1
	argonThreads    uint8  = 4
	argonSaltLength int    = 16
	argonKeyLength  uint32 = 32
)

// ErrInvalidPasswordHash is returned when an encoded hash cannot be parsed.
var ErrInvalidPasswordHash = errors.New("authn: invalid password hash encoding")

// dummyHash is a pre-computed argon2id hash used to equalise timing for
// non-existent users. The actual password doesn't matter because the
// comparison will always fail.
var dummyHash string

func init() {
	h, err := HashPassword("nodate-shared-dummy-password-timing-equaliser")
	if err != nil {
		panic("authn: failed to generate dummy hash: " + err.Error())
	}
	dummyHash = h
}

// DummyHash returns a pre-computed argon2id hash that can be verified
// against any password to equalise timing for non-existent users. The
// comparison will always fail, but the time spent is indistinguishable
// from a real verification.
func DummyHash() string { return dummyHash }

// HashPassword hashes the given plaintext password with argon2id and returns
// the canonical encoded form ($argon2id$v=19$...).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("authn: read salt: %w", err)
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
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want))) //#nosec G115 -- the decoded hash is a fixed-width digest, so its length fits uint32
	if subtle.ConstantTimeCompare(want, got) == 1 {
		return true, nil
	}
	return false, nil
}
