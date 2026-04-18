package auth

import "github.com/nodate-flow/nodate-flow/packages/go-shared/authn"

// PrefixRefresh is the user-visible prefix for refresh tokens.
const PrefixRefresh = authn.PrefixRefresh

// GenerateRefresh generates a fresh refresh token and returns its
// plaintext form ("rfr_<hex>") alongside the hex SHA-256 hash that
// should be stored in the sessions table.
func GenerateRefresh() (plaintext string, hash string, err error) {
	return authn.GenerateRefresh()
}

// HashOpaque returns the hex SHA-256 of the given plaintext token.
func HashOpaque(plaintext string) string {
	return authn.HashOpaque(plaintext)
}
