package auth

import (
	"net/http"
	"time"
)

// refreshCookieName is the name of the httpOnly refresh-token cookie.
const refreshCookieName = "nd_rt"

// refreshCookiePath scopes the refresh cookie to /auth endpoints so it
// is never sent alongside regular API calls, minimising CSRF surface.
const refreshCookiePath = "/auth"

// refreshCookieTTL mirrors the server-side session row lifetime used by
// CreateSession / RotateSessionRefreshHash (30 days).
const refreshCookieTTL = 30 * 24 * time.Hour

// refreshCookieSameSite returns the SameSite mode that pairs with the
// given Secure flag. The web app and the api are served from different
// origins (e.g. http://localhost:5173 → http://localhost:8080 in dev,
// https://app.example.com → https://api.example.com in prod), so the
// refresh cookie has to survive a cross-site fetch on boot.
//
//   - secure=true  (https/prod): SameSite=None is the only mode that
//     allows cross-site sending; the spec also requires Secure, which
//     is already set.
//   - secure=false (http/dev):   SameSite=None is rejected by Chrome
//     without Secure, so we fall back to Lax. Lax is enough for the
//     localhost dev case because Chrome treats different ports on the
//     same host as same-site, and the cookie is only ever read by the
//     POST /auth/refresh fetch, which Lax permits for same-site
//     requests.
func refreshCookieSameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

// newRefreshCookie builds a Set-Cookie value carrying the rotated
// refresh token. The cookie is HttpOnly + scoped to /auth; Secure and
// SameSite are derived from cfg so local http dev still works while
// production https remains cross-site safe.
func newRefreshCookie(token string, secure bool) http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   int(refreshCookieTTL.Seconds()),
	}
}

// oidcStateCookieName carries the verifier that binds an OIDC state
// parameter to the browser that started the flow.
const oidcStateCookieName = "nd_oidc"

// oidcStateCookiePath scopes the state cookie to the OIDC endpoints, so
// it never rides along with any other request.
const oidcStateCookiePath = "/auth/oidc"

// newOIDCStateCookie builds the Set-Cookie carrying the state verifier.
// Secure / SameSite mirror the refresh cookie: the cookie is written by
// a cross-site fetch from accounts-web and read back on the top-level
// redirect the identity provider performs, and SameSite=Lax permits that
// navigation while still keeping the cookie off cross-site subresource
// requests.
func newOIDCStateCookie(verifier string, expiresAt time.Time, secure bool) http.Cookie {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     oidcStateCookieName,
		Value:    verifier,
		Path:     oidcStateCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   maxAge,
	}
}

// clearedOIDCStateCookie evicts the state cookie once the flow ends.
// Attributes must mirror newOIDCStateCookie or the browser treats this
// as a different cookie and keeps the original.
func clearedOIDCStateCookie(secure bool) http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     oidcStateCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   -1,
	}
}

// clearedRefreshCookie builds a Set-Cookie value that deletes the
// refresh cookie on the client (MaxAge=-1 emits Max-Age=0). The
// SameSite/Secure attributes must mirror newRefreshCookie or browsers
// treat it as a different cookie and refuse to evict the original.
func clearedRefreshCookie(secure bool) http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: refreshCookieSameSite(secure),
		MaxAge:   -1,
	}
}
