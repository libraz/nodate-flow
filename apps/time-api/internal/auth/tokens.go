package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const opaqueTokenBytes = 32

// PrefixRefresh is the user-visible prefix for refresh tokens.
const PrefixRefresh = "rfr_"

// GenerateRefresh generates a fresh refresh token and returns its
// plaintext form ("rfr_<hex>") alongside the hex SHA-256 hash that
// should be stored in the sessions table.
func GenerateRefresh() (plaintext string, hash string, err error) {
	buf := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: read rand: %w", err)
	}
	body := hex.EncodeToString(buf)
	plaintext = PrefixRefresh + body
	hash = HashOpaque(plaintext)
	return plaintext, hash, nil
}

// HashOpaque returns the hex SHA-256 of the given plaintext token.
func HashOpaque(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
