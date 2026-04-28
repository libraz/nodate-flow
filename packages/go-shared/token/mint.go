// Package token provides shared opaque-token primitives for capability
// URLs (public share links, invite magic links). The plaintext token is
// 32 random bytes encoded as base64url (no padding); the hash returned
// alongside is the hex-encoded SHA-256 of the plaintext, which is what
// gets persisted in the database.
//
// Use this package whenever a feature needs a single-shot URL token
// that is hashed at rest. Prefer authn.GenerateOpaque for prefixed
// auth-bearer tokens (PAT / MCP / refresh) — those have their own
// alphabet and live alongside the session machinery.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// TokenEntropyBytes is the byte length used for the random body of a
// freshly minted token. 32 bytes (256 bits) is the same entropy budget
// authn uses for refresh / PAT, well above the 128-bit floor we need
// to make brute-forcing a stored token hash infeasible.
const TokenEntropyBytes = 32

// MintToken returns (raw, hash, err). raw is a 32-byte random value
// encoded as unpadded base64url and is what the caller hands to the
// recipient (share URL / email magic link). hash is hex-encoded
// SHA-256 of raw and is what gets persisted in the *_token_hash
// column.
//
// The plaintext is intentionally not prefixed: capability tokens live
// inside opaque URLs / form payloads where the consumer never inspects
// them, so a prefix would only be visual noise. Use ValidatePrefix /
// the prefix constants only for bearer tokens that the parser must
// branch on (PAT vs MCP, etc.).
func MintToken() (raw, hash string, err error) {
	buf := make([]byte, TokenEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("token: read rand: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	hash = HashToken(raw)
	return raw, hash, nil
}

// HashToken returns the hex-encoded SHA-256 of raw. The hex encoding
// keeps the column type a printable VARCHAR(64) so query plans match
// across MySQL versions; storing as raw BINARY would save 32 bytes per
// row but lose the round-trippable wire shape used by the auth audit
// log.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
