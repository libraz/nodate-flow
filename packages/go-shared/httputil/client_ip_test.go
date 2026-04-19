package httputil

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For single IP",
			xff:        "192.168.1.100",
			remoteAddr: "10.0.0.1:12345",
			want:       "192.168.1.100",
		},
		{
			name:       "X-Forwarded-For multiple IPs picks first",
			xff:        "203.0.113.50, 70.41.3.18, 150.172.238.178",
			remoteAddr: "10.0.0.1:12345",
			want:       "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For with whitespace",
			xff:        "  198.51.100.1 , 192.168.1.1",
			remoteAddr: "10.0.0.1:12345",
			want:       "198.51.100.1",
		},
		{
			name:       "no XFF falls back to RemoteAddr host",
			xff:        "",
			remoteAddr: "172.16.0.5:8080",
			want:       "172.16.0.5",
		},
		{
			name:       "RemoteAddr without port",
			xff:        "",
			remoteAddr: "172.16.0.5",
			want:       "172.16.0.5",
		},
		{
			name:       "IPv6 RemoteAddr with port",
			xff:        "",
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
			got := ExtractClientIP(r)
			assert.Equal(t, tc.want, got)
		})
	}
}
