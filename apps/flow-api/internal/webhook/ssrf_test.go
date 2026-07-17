package webhook

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// strictMode clears the NF_WEBHOOK_ALLOW_PRIVATE escape hatch so the
// full SSRF policy is exercised regardless of the ambient environment.
// t.Setenv also guards against accidental t.Parallel usage in callers.
func strictMode(t *testing.T) {
	t.Helper()
	t.Setenv("NF_WEBHOOK_ALLOW_PRIVATE", "")
}

// TestValidateURLRejectsDisallowed asserts that the create-time /
// test-time gate rejects every URL class the SSRF policy forbids:
// non-https schemes, embedded credentials, and hosts that are (or
// resolve to) non-public addresses such as the cloud metadata endpoint.
func TestValidateURLRejectsDisallowed(t *testing.T) {
	strictMode(t)
	ctx := context.Background()

	cases := []struct {
		name string
		url  string
	}{
		{"cloud metadata over http", "http://169.254.169.254/latest/meta-data/"},
		{"private network over http", "http://10.0.0.1/hook"},
		{"loopback over http", "http://127.0.0.1/hook"},
		{"cloud metadata over https", "https://169.254.169.254/latest/meta-data/"},
		{"private network over https", "https://10.0.0.1/hook"},
		{"loopback over https", "https://127.0.0.1/hook"},
		{"userinfo", "https://user:pass@example.com/hook"},
		{"userinfo without password", "https://user@example.com/hook"},
		{"loopback hostname", "https://localhost/hook"},
		{"ipv6 loopback", "https://[::1]/hook"},
		{"ipv6 link-local", "https://[fe80::1]/hook"},
		{"ipv6 unique-local", "https://[fd00::1]/hook"},
		{"unspecified", "https://0.0.0.0/hook"},
		{"this-network block", "https://0.1.2.3/hook"},
		{"broadcast", "https://255.255.255.255/hook"},
		{"carrier-grade nat", "https://100.64.0.1/hook"},
		{"multicast", "https://224.0.0.1/hook"},
		{"link-local", "https://169.254.1.1/hook"},
		{"plain http public host", "http://example.com/hook"},
		{"ftp scheme", "ftp://example.com/hook"},
		{"empty host", "https:///hook"},
		{"not a url", "://nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(ctx, tc.url)
			if err == nil {
				t.Fatalf("ValidateURL(%q) = nil, want ErrURLDisallowed", tc.url)
			}
			if !errors.Is(err, ErrURLDisallowed) {
				t.Fatalf("ValidateURL(%q) = %v, want ErrURLDisallowed", tc.url, err)
			}
		})
	}
}

// TestIsDisallowedIPAllowsPublic asserts the denylist does not
// over-block well-known public unicast addresses.
func TestIsDisallowedIPAllowsPublic(t *testing.T) {
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if isDisallowedIP(net.ParseIP(s)) {
			t.Errorf("isDisallowedIP(%s) = true, want false", s)
		}
	}
	if !isDisallowedIP(nil) {
		t.Error("isDisallowedIP(nil) = false, want true")
	}
}

// TestSafeClientBlocksPrivateDialTarget simulates DNS rebinding: a
// hostname that passes any earlier syntactic checks but resolves to a
// loopback address at connect time. The dialer Control hook must
// refuse the connect even though a live server is listening there.
func TestSafeClientBlocksPrivateDialTarget(t *testing.T) {
	strictMode(t)

	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewSafeClient(5 * time.Second)

	// srv.URL is http://127.0.0.1:PORT — a literal loopback target.
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("request to loopback target succeeded, want dial-time rejection")
	}
	if !errors.Is(err, ErrURLDisallowed) {
		t.Fatalf("error = %v, want chain containing ErrURLDisallowed", err)
	}

	// Rebind simulation: "localhost" resolves to 127.0.0.1/::1 at dial
	// time, so the connect-time check must reject it as well.
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	resp, err = client.Get("http://localhost:" + port + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("request to localhost target succeeded, want dial-time rejection")
	}
	if !errors.Is(err, ErrURLDisallowed) {
		t.Fatalf("error = %v, want chain containing ErrURLDisallowed", err)
	}

	if hit {
		t.Fatal("target server received a request; the dialer let a disallowed connect through")
	}
}

// TestSafeClientRedirectPolicy asserts the CheckRedirect hook rejects
// redirects toward disallowed destinations and caps chain length.
func TestSafeClientRedirectPolicy(t *testing.T) {
	strictMode(t)

	client := NewSafeClient(5 * time.Second)

	mkReq := func(url string) *http.Request {
		req, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		return req
	}
	via := []*http.Request{mkReq("https://example.com/hook")}

	if err := client.CheckRedirect(mkReq("https://127.0.0.1/steal"), via); !errors.Is(err, ErrURLDisallowed) {
		t.Errorf("redirect to loopback: err = %v, want ErrURLDisallowed", err)
	}
	if err := client.CheckRedirect(mkReq("http://example.com/hook"), via); !errors.Is(err, ErrURLDisallowed) {
		t.Errorf("redirect downgrade to http: err = %v, want ErrURLDisallowed", err)
	}
	if err := client.CheckRedirect(mkReq("https://u:p@example.com/hook"), via); !errors.Is(err, ErrURLDisallowed) {
		t.Errorf("redirect with userinfo: err = %v, want ErrURLDisallowed", err)
	}

	long := make([]*http.Request, maxRedirects)
	for i := range long {
		long[i] = mkReq("https://example.com/hook")
	}
	if err := client.CheckRedirect(mkReq("https://example.com/hook"), long); !errors.Is(err, ErrURLDisallowed) {
		t.Errorf("redirect chain over limit: err = %v, want ErrURLDisallowed", err)
	}
}

// TestValidateURLPermissiveMode asserts the NF_WEBHOOK_ALLOW_PRIVATE
// escape hatch admits loopback http targets (used by local development
// and the e2e delivery tests) while still rejecting credentials.
func TestValidateURLPermissiveMode(t *testing.T) {
	t.Setenv("NF_WEBHOOK_ALLOW_PRIVATE", "1")
	ctx := context.Background()

	if err := ValidateURL(ctx, "http://127.0.0.1:8080/hook"); err != nil {
		t.Errorf("permissive loopback: err = %v, want nil", err)
	}
	if err := ValidateURL(ctx, "https://example.com/hook"); err != nil {
		t.Errorf("permissive public: err = %v, want nil", err)
	}
	if err := ValidateURL(ctx, "https://user:pass@example.com/hook"); !errors.Is(err, ErrURLDisallowed) {
		t.Errorf("permissive userinfo: err = %v, want ErrURLDisallowed", err)
	}
}
