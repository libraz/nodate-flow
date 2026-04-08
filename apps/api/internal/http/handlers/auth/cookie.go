package auth

import (
	"net/http"
	"time"
)

// refreshCookieName is the name of the httpOnly refresh-token cookie.
const refreshCookieName = "nf_rt"

// refreshCookiePath scopes the refresh cookie to /auth endpoints so it
// is never sent alongside regular API calls, minimising CSRF surface.
const refreshCookiePath = "/auth"

// refreshCookieTTL mirrors the server-side session row lifetime used by
// CreateSession / RotateSessionRefreshHash (30 days).
const refreshCookieTTL = 30 * 24 * time.Hour

// newRefreshCookie builds a Set-Cookie value carrying the rotated
// refresh token. The cookie is HttpOnly + SameSite=Strict + scoped to
// /auth; Secure is toggled by cfg so local http dev still works.
func newRefreshCookie(token string, secure bool) http.Cookie {
	return http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(refreshCookieTTL.Seconds()),
	}
}

// clearedRefreshCookie builds a Set-Cookie value that deletes the
// refresh cookie on the client (MaxAge=-1 emits Max-Age=0).
func clearedRefreshCookie(secure bool) http.Cookie {
	return http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}
}
