// Package crypto is the only place in the api binary that holds the symmetric
// master key used to seal LLM provider API keys at rest.
//
// SECURITY POLICY
//
// Decrypt MUST NOT be called from anywhere outside this package and the
// internal/ai/providers/* package family. The depguard linter in
// apps/api/.golangci.yml enforces that. The plaintext returned by Decrypt
// must be passed straight to the upstream provider's Authorization header
// and never stored in long-lived structs, logs, errors, or responses.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/hkdf"
)

// EnvVar is the name of the environment variable that holds the master key.
const EnvVar = "NF_SECRET_KEY"

// hkdfInfo is the domain-separation label used when deriving the AES key
// from the master secret. Bump the version suffix when rotating the
// derivation scheme (NOT for ordinary key rotations).
const hkdfInfo = "nodate-flow:ai:v1"

// nonceSize is the GCM standard nonce length.
const nonceSize = 12

// Cipher seals and opens secret blobs with AES-256-GCM under a key derived
// from NF_SECRET_KEY via HKDF-SHA256. The returned blob layout is
// nonce || ciphertext-with-tag, where the GCM tag is appended to the
// ciphertext by gcm.Seal as the trailing 16 bytes.
type Cipher struct {
	gcm cipher.AEAD
}

// NewFromEnv reads NF_SECRET_KEY from the process environment, derives a
// 32-byte AES key via HKDF-SHA256 with a fixed domain-separation label,
// and returns a ready Cipher. It is the only public way to construct a
// Cipher; tests use New for explicit key injection.
//
// The master key MUST be a 32-byte secret encoded as either 64-char hex or
// standard base64 (with or without padding). Anything else is rejected at
// startup so the api binary never runs with a misconfigured key.
func NewFromEnv() (*Cipher, error) {
	raw := os.Getenv(EnvVar)
	if raw == "" {
		return nil, fmt.Errorf("crypto: %s is not set", EnvVar)
	}
	master, err := decodeMasterKey(raw)
	if err != nil {
		return nil, err
	}
	return New(master)
}

// New builds a Cipher from a 32-byte master secret. Prefer NewFromEnv in
// production wiring; New exists for tests and the secrets-rotate CLI.
func New(master []byte) (*Cipher, error) {
	if len(master) != 32 {
		return nil, fmt.Errorf("crypto: master key must be 32 bytes, got %d", len(master))
	}
	r := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	derived := make([]byte, 32)
	if _, err := io.ReadFull(r, derived); err != nil {
		return nil, fmt.Errorf("crypto: hkdf: %w", err)
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt seals plaintext under the cipher's key. The returned blob is
// safe to store in a VARBINARY column and contains a fresh random nonce.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}
	sealed := c.gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt opens a blob produced by Encrypt and returns the plaintext.
//
// IMPORTANT: callers must drop the returned plaintext as soon as possible.
// Do not place it in struct fields, do not log it, and do not return it
// to API clients.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	if len(blob) < nonceSize+16 {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce := blob[:nonceSize]
	ct := blob[nonceSize:]
	plain, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	return plain, nil
}

// APIKeyPrefix returns the first 8 characters of an LLM provider API key
// for masked display (e.g. "sk-ant-A"). Shorter keys return what they have.
func APIKeyPrefix(plaintext string) string {
	if len(plaintext) <= 8 {
		return plaintext
	}
	return plaintext[:8]
}

// APIKeySuffix returns the last 4 characters of an LLM provider API key
// for masked display (e.g. "AbCd"). Shorter keys return what they have.
func APIKeySuffix(plaintext string) string {
	if len(plaintext) <= 4 {
		return plaintext
	}
	return plaintext[len(plaintext)-4:]
}

func decodeMasterKey(raw string) ([]byte, error) {
	if len(raw) == 64 {
		if b, err := hex.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("crypto: %s must be 32-byte hex or base64", EnvVar)
}
