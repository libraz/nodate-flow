package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveWithSecurity runs a no-op handler wrapped in SecurityHeaders for
// the given request path and returns the recorded response header.
func serveWithSecurity(t *testing.T, path string) http.Header {
	t.Helper()
	h := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.ServeHTTP(rec, req)
	return rec.Result().Header
}

// TestSecurityHeaders_DeniesFramingByDefault asserts that ordinary paths
// keep the deny-all framing headers.
func TestSecurityHeaders_DeniesFramingByDefault(t *testing.T) {
	for _, path := range []string{"/me", "/health", "/workspaces/x/calendars", "/share/calendars"} {
		hdr := serveWithSecurity(t, path)
		if got := hdr.Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("path %q: X-Frame-Options = %q, want DENY", path, got)
		}
		if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("path %q: CSP = %q, want frame-ancestors 'none'", path, csp)
		}
	}
}

// TestSecurityHeaders_RelaxesPublicShareFraming asserts that the public
// calendar-share render path drops X-Frame-Options and widens the CSP
// frame-ancestors directive to the default permissive value.
func TestSecurityHeaders_RelaxesPublicShareFraming(t *testing.T) {
	hdr := serveWithSecurity(t, "/share/cal/some-token")
	if got := hdr.Get("X-Frame-Options"); got != "" {
		t.Fatalf("public share: X-Frame-Options = %q, want empty", got)
	}
	csp := hdr.Get("Content-Security-Policy")
	if strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("public share: CSP = %q, must not deny all frame-ancestors", csp)
	}
	if !strings.Contains(csp, "frame-ancestors *") {
		t.Fatalf("public share: CSP = %q, want default frame-ancestors *", csp)
	}
}

// TestSecurityHeaders_FrameAncestorsConfigurable asserts that
// NF_EMBED_FRAME_ANCESTORS overrides the public-share frame-ancestors
// value, e.g. tightening it to same-origin.
func TestSecurityHeaders_FrameAncestorsConfigurable(t *testing.T) {
	t.Setenv(embedFrameAncestorsEnv, "'self' https://embed.example")
	hdr := serveWithSecurity(t, "/share/cal/some-token")
	csp := hdr.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'self' https://embed.example") {
		t.Fatalf("public share: CSP = %q, want configured frame-ancestors", csp)
	}
	// A normal path still denies regardless of the override.
	normal := serveWithSecurity(t, "/me")
	if !strings.Contains(normal.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("normal path must still deny framing, CSP = %q", normal.Get("Content-Security-Policy"))
	}
}
