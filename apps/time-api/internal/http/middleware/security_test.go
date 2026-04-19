package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurityHeaders_SetsAllHeaders(t *testing.T) {
	t.Parallel()

	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	h := rec.Header()
	require.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", h.Get("X-Frame-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"))
	require.Equal(t, "max-age=63072000; includeSubDomains; preload", h.Get("Strict-Transport-Security"))
	require.Empty(t, h.Get("X-XSS-Protection"), "deprecated X-XSS-Protection must not be set")

	csp := h.Get("Content-Security-Policy")
	require.Contains(t, csp, "default-src 'self'")
	require.Contains(t, csp, "script-src 'self'")
	require.Contains(t, csp, "style-src 'self' 'nonce-")
	require.Contains(t, csp, "frame-ancestors 'none'")
}

func TestSecurityHeaders_NonceInContext(t *testing.T) {
	t.Parallel()

	var nonce string
	handler := SecurityHeaders()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = NonceFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.NotEmpty(t, nonce, "nonce must be set in context")

	// Nonce must appear in the CSP header.
	csp := rec.Header().Get("Content-Security-Policy")
	require.True(t, strings.Contains(csp, "'nonce-"+nonce+"'"), "CSP must contain the generated nonce")
}
