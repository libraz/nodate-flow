package auth

import (
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	sharedtoken "github.com/libraz/nodate-flow/packages/go-shared/token"
)

// PrefixPAT is the user-visible prefix for personal access tokens.
// Sourced from packages/go-shared/token so auth-api and flow-api see
// the same catalogue.
const PrefixPAT = sharedtoken.PrefixPAT

// PrefixMCP is the user-visible prefix for MCP bearer tokens.
const PrefixMCP = sharedtoken.PrefixMCP

// PrefixRefresh is the user-visible prefix for refresh tokens.
const PrefixRefresh = authn.PrefixRefresh

// GenerateOpaque returns a (plaintext, hash) pair where plaintext is
// "<prefix><hex>" and hash is the hex SHA-256 of the plaintext (after
// the prefix). The hash is what gets stored in the database.
func GenerateOpaque(prefix string) (plaintext string, hash string, err error) {
	return authn.GenerateOpaque(prefix)
}

// HashOpaque returns the hex SHA-256 of the given plaintext token. The
// full plaintext (including prefix) is hashed so that prefix changes
// invalidate old hashes automatically.
func HashOpaque(plaintext string) string {
	return authn.HashOpaque(plaintext)
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
	return authn.GenerateRefresh()
}
