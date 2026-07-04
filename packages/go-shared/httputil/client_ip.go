// Package httputil provides shared HTTP utility functions used across
// all nodate-flow API services.
package httputil

import (
	"net"
	"net/http"
	"strings"
)

// ExtractClientIP extracts the caller's IP address from an HTTP request.
// It intentionally ignores unauthenticated forwarding headers and strips
// the port from r.RemoteAddr when present.
func ExtractClientIP(r *http.Request) string {
	return extractRemoteAddrHost(r.RemoteAddr)
}

// ExtractClientIPWithTrustedProxyHops extracts the caller's IP address
// when the service is deployed behind a fixed number of trusted reverse
// proxies. A hop count of zero (the default) ignores X-Forwarded-For and
// X-Real-Ip entirely. For X-Forwarded-For, the selected client address is
// the entry immediately before the trusted proxy hops counted from the
// right side of the header, which prevents attacker-supplied leading
// entries from becoming the rate-limit key. X-Real-Ip is trusted only for
// the single-proxy case.
func ExtractClientIPWithTrustedProxyHops(r *http.Request, trustedProxyHops int) string {
	remote := ExtractClientIP(r)
	if trustedProxyHops <= 0 {
		return remote
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := splitForwardedFor(xff)
		if trustedProxyHops <= len(parts) {
			selected := parts[len(parts)-trustedProxyHops]
			if selected != "" {
				return selected
			}
		}
	}
	if trustedProxyHops == 1 {
		if xri := strings.TrimSpace(r.Header.Get("X-Real-Ip")); xri != "" {
			return xri
		}
	}
	return remote
}

func splitForwardedFor(xff string) []string {
	raw := strings.Split(xff, ",")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func extractRemoteAddrHost(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
