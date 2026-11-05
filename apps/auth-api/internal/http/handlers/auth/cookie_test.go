package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCookiePaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		prefix      string
		wantRefresh string
		wantOIDC    string
	}{
		{"prefixed", "/auth-api", "/auth-api/auth", "/auth-api/auth/oidc"},
		{"root", "", "/auth", "/auth/oidc"},
	}
	for _, tt := range tests {
		for _, secure := range []bool{true, false} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				d := Deps{CookiePathPrefix: tt.prefix, CookieSecure: secure}
				exp := time.Now().Add(time.Minute)

				cookies := map[string]http.Cookie{
					"refresh":         d.newRefreshCookie("tok"),
					"refresh cleared": d.clearedRefreshCookie(),
					"oidc":            d.newOIDCStateCookie("v", exp),
					"oidc cleared":    d.clearedOIDCStateCookie(),
				}
				wantPath := map[string]string{
					"refresh":         tt.wantRefresh,
					"refresh cleared": tt.wantRefresh,
					"oidc":            tt.wantOIDC,
					"oidc cleared":    tt.wantOIDC,
				}
				for name, c := range cookies {
					assert.Equal(t, wantPath[name], c.Path, name)
					assert.Equal(t, http.SameSiteLaxMode, c.SameSite, name)
					assert.Equal(t, secure, c.Secure, name)
					assert.True(t, c.HttpOnly, name)
				}
			})
		}
	}
}
