package httputil

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractClientIP_DefaultIgnoresForwardingHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		want       string
	}{
		{
			name:       "forged X-Forwarded-For ignored",
			xff:        "192.168.1.100",
			remoteAddr: "10.0.0.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "forged X-Forwarded-For chain ignored",
			xff:        "203.0.113.50, 70.41.3.18, 150.172.238.178",
			remoteAddr: "10.0.0.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Real-Ip ignored",
			xff:        "203.0.113.50",
			xri:        "10.0.0.99",
			remoteAddr: "10.0.0.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "no XFF or XRI falls back to RemoteAddr host",
			remoteAddr: "172.16.0.5:8080",
			want:       "172.16.0.5",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "172.16.0.5",
			want:       "172.16.0.5",
		},
		{
			name:       "IPv6 RemoteAddr with port",
			remoteAddr: "[::1]:8080",
			want:       "::1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{
				RemoteAddr: tc.remoteAddr,
				Header:     http.Header{},
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				r.Header.Set("X-Real-Ip", tc.xri)
			}
			got := ExtractClientIP(r)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractClientIPWithTrustedProxyHops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		hops       int
		xff        string
		xri        string
		remoteAddr string
		want       string
	}{
		{
			name:       "one trusted proxy selects rightmost XFF entry",
			hops:       1,
			xff:        "198.51.100.111, 203.0.113.50",
			remoteAddr: "10.0.0.1:12345",
			want:       "203.0.113.50",
		},
		{
			name:       "two trusted proxies select entry before two proxy hops",
			hops:       2,
			xff:        "198.51.100.111, 203.0.113.50, 70.41.3.18",
			remoteAddr: "10.0.0.1:12345",
			want:       "203.0.113.50",
		},
		{
			name:       "classic single-proxy header still resolves original client",
			hops:       1,
			xff:        "203.0.113.50",
			remoteAddr: "10.0.0.1:12345",
			want:       "203.0.113.50",
		},
		{
			name:       "single trusted proxy may use X-Real-Ip without XFF",
			hops:       1,
			xri:        "  10.0.0.99 ",
			remoteAddr: "172.16.0.5:8080",
			want:       "10.0.0.99",
		},
		{
			name:       "insufficient XFF entries falls back to RemoteAddr",
			hops:       2,
			xff:        "203.0.113.50",
			remoteAddr: "10.0.0.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "zero hops ignores XFF",
			hops:       0,
			xff:        "203.0.113.50",
			remoteAddr: "10.0.0.1:12345",
			want:       "10.0.0.1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{
				RemoteAddr: tc.remoteAddr,
				Header:     http.Header{},
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				r.Header.Set("X-Real-Ip", tc.xri)
			}
			got := ExtractClientIPWithTrustedProxyHops(r, tc.hops)
			assert.Equal(t, tc.want, got)
		})
	}
}
