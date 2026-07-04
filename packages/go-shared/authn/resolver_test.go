package authn

import (
	"os"
	"strings"
	"testing"
)

func TestJWTResolverChecksSessionStateForSIDClaims(t *testing.T) {
	src, err := os.ReadFile("resolver.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, needle := range []string{
		"claims.SessionPublicID != zero",
		"FROM sessions",
		"public_id = ?",
		"user_id = ?",
		"enabled = TRUE",
		"revoked_at IS NULL",
		"expires_at > CURRENT_TIMESTAMP",
		"ErrTokenInvalid",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("JWT session validation is missing %q", needle)
		}
	}
}
