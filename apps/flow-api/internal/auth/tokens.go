package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Token byte length for opaque tokens (refresh / PAT / MCP).
const opaqueTokenBytes = 32

// PrefixPAT is the user-visible prefix for personal access tokens.
const PrefixPAT = "pat_"

// PrefixMCP is the user-visible prefix for MCP bearer tokens.
const PrefixMCP = "mcp_"

// PrefixRefresh is the user-visible prefix for refresh tokens.
const PrefixRefresh = "rfr_"

// GenerateOpaque returns a (plaintext, hash) pair where plaintext is
// "<prefix><hex>" and hash is the hex SHA-256 of the plaintext (after
// the prefix). The hash is what gets stored in the database.
func GenerateOpaque(prefix string) (plaintext string, hash string, err error) {
	buf := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: read rand: %w", err)
	}
	body := hex.EncodeToString(buf)
	plaintext = prefix + body
	hash = HashOpaque(plaintext)
	return plaintext, hash, nil
}

// HashOpaque returns the hex SHA-256 of the given plaintext token. The
// full plaintext (including prefix) is hashed so that prefix changes
// invalidate old hashes automatically.
func HashOpaque(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// GeneratePAT generates a fresh personal access token.
func GeneratePAT() (plaintext string, hash string, err error) {
	return GenerateOpaque(PrefixPAT)
}

// GenerateMCP generates a fresh MCP bearer token.
func GenerateMCP() (plaintext string, hash string, err error) {
	return GenerateOpaque(PrefixMCP)
}

// GenerateRefresh generates a fresh refresh token.
func GenerateRefresh() (plaintext string, hash string, err error) {
	return GenerateOpaque(PrefixRefresh)
}
