// Package httputil provides shared HTTP utility functions used across
// all nodate-flow API services.
package httputil

import (
	"net"
	"net/http"
	"strings"
)

// ExtractClientIP extracts the caller's IP address from an HTTP request.
// It reads the first hop from X-Forwarded-For when present, falls back
// to X-Real-Ip, and finally strips the port from r.RemoteAddr.
func ExtractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First hop is the original client.
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
