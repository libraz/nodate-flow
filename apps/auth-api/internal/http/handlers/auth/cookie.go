package auth

import (
	"net/http"
	"time"
)

// refreshCookieName is the name of the httpOnly refresh-token cookie.
const refreshCookieName = "nd_rt"

// refreshCookiePath scopes the refresh cookie to /auth endpoints so it
// is never sent alongside regular API calls, minimising CSRF surface.
// Deps.CookiePathPrefix is prepended when auth-api is published under a
// path prefix.
const refreshCookiePath = "/auth"

// refreshCookieTTL mirrors the server-side session row lifetime used by
// CreateSession / RotateSessionRefreshHash (30 days).
const refreshCookieTTL = 30 * 24 * time.Hour

// refreshCookieSameSite is Lax whatever the Secure flag. Production serves
// the web apps and auth-api from one origin and dev serves them from
// localhost ports, which browsers treat as same-site, so the cookie is
// never needed on a cross-site request.
func refreshCookieSameSite() http.SameSite {
	return http.SameSiteLaxMode
}

// newRefreshCookie builds a Set-Cookie value carrying the rotated
// refresh token. The cookie is HttpOnly + scoped to the /auth endpoints
// under the configured path prefix; Secure follows cfg so local http dev
// still works.
func (d Deps) newRefreshCookie(token string) http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     refreshCookieName,
		Value:    token,
		Path:     d.CookiePathPrefix + refreshCookiePath,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: refreshCookieSameSite(),
		MaxAge:   int(refreshCookieTTL.Seconds()),
	}
}

// oidcStateCookieName carries the verifier that binds an OIDC state
// parameter to the browser that started the flow.
const oidcStateCookieName = "nd_oidc"

// oidcStateCookiePath scopes the state cookie to the OIDC endpoints, so
// it never rides along with any other request. Deps.CookiePathPrefix is
// prepended like for the refresh cookie.
const oidcStateCookiePath = "/auth/oidc"

// newOIDCStateCookie builds the Set-Cookie carrying the state verifier.
// Secure / SameSite mirror the refresh cookie: SameSite=Lax lets the
// cookie come back on the top-level redirect the identity provider
// performs while keeping it off cross-site subresource requests.
func (d Deps) newOIDCStateCookie(verifier string, expiresAt time.Time) http.Cookie {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     oidcStateCookieName,
		Value:    verifier,
		Path:     d.CookiePathPrefix + oidcStateCookiePath,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: refreshCookieSameSite(),
		MaxAge:   maxAge,
	}
}

// clearedOIDCStateCookie evicts the state cookie once the flow ends.
// Attributes must mirror newOIDCStateCookie or the browser treats this
// as a different cookie and keeps the original.
func (d Deps) clearedOIDCStateCookie() http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     d.CookiePathPrefix + oidcStateCookiePath,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: refreshCookieSameSite(),
		MaxAge:   -1,
	}
}

// clearedRefreshCookie builds a Set-Cookie value that deletes the
// refresh cookie on the client (MaxAge=-1 emits Max-Age=0). The
// SameSite/Secure attributes must mirror newRefreshCookie or browsers
// treat it as a different cookie and refuse to evict the original.
func (d Deps) clearedRefreshCookie() http.Cookie {
	return http.Cookie{ //#nosec G124 -- HttpOnly always set; Secure/SameSite intentionally derived from cfg (http dev vs https prod)
		Name:     refreshCookieName,
		Value:    "",
		Path:     d.CookiePathPrefix + refreshCookiePath,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: refreshCookieSameSite(),
		MaxAge:   -1,
	}
}
