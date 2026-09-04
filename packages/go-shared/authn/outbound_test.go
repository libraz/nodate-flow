package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// TestOutboundClientCarriesADeadline pins the install itself.
//
// Everything else in this file rests on it: go-oidc and oauth2 read the
// client out of the context under one key, and a context that carries
// nothing under that key sends them to http.DefaultClient, which has no
// deadline. So the assertion is on the value the libraries will find, not
// on the helper having been called.
func TestOutboundClientCarriesADeadline(t *testing.T) {
	t.Parallel()

	installed, ok := WithOutboundHTTPClient(context.Background()).Value(oauth2.HTTPClient).(*http.Client)
	if !ok {
		t.Fatal("WithOutboundHTTPClient installed nothing under oauth2.HTTPClient, " +
			"so go-oidc and oauth2 will both fall back to http.DefaultClient")
	}
	if installed.Timeout <= 0 {
		t.Fatalf("the installed client has a timeout of %v; an outbound call with no "+
			"deadline is held for as long as the peer keeps the socket open", installed.Timeout)
	}
}

// TestDiscoveryRunsOnceAndIsNotStampeded covers the success path and the
// property that makes it safe under load: concurrent callers arriving
// before the first attempt finishes queue behind it rather than each
// opening their own connection to the issuer.
func TestDiscoveryRunsOnceAndIsNotStampeded(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	issuer := newIssuer(t, func() (int, bool) {
		attempts.Add(1)
		time.Sleep(20 * time.Millisecond)
		return http.StatusOK, true
	})

	var d Discovery
	var built atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Do(context.Background(), issuer, func(*oidc.Provider) {
				built.Add(1)
			}); err != nil {
				t.Errorf("discovery against a healthy issuer failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := attempts.Load(); got != 1 {
		t.Errorf("eight concurrent callers made %d discovery requests, want 1; "+
			"a slow issuer collects one connection per sign-in when they are not serialised", got)
	}
	if got := built.Load(); got != 1 {
		t.Errorf("build ran %d times, want 1", got)
	}
}

// TestDiscoveryRetriesAfterTheCooldown is the reason discovery does not
// use sync.Once.
//
// A Once caches the first error for the life of the process, so a single
// unreachable moment at boot leaves sign-in dead until somebody restarts
// the binary. The cooldown is the other half: without it every sign-in
// during an outage would open its own connection to an issuer that is
// already failing.
func TestDiscoveryRetriesAfterTheCooldown(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	healthy := false
	issuer := newIssuer(t, func() (int, bool) {
		attempts.Add(1)
		if !healthy {
			return http.StatusInternalServerError, false
		}
		return http.StatusOK, true
	})

	clock := time.Now()
	d := Discovery{RetryAfter: time.Minute, Now: func() time.Time { return clock }}
	build := 0
	run := func() error {
		return d.Do(context.Background(), issuer, func(*oidc.Provider) { build++ })
	}

	first := run()
	if first == nil {
		t.Fatal("discovery against an issuer answering 500 reported success")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("first call made %d requests, want 1", got)
	}

	// Inside the cooldown the cached failure answers, so a provider that
	// is down is asked once per cooldown rather than once per sign-in.
	clock = clock.Add(59 * time.Second)
	if err := run(); err == nil {
		t.Fatal("a call inside the cooldown reported success")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("a call inside the cooldown made %d requests in total, want 1", got)
	}

	// Past it, the failure is not permanent.
	healthy = true
	clock = clock.Add(2 * time.Second)
	if err := run(); err != nil {
		t.Fatalf("discovery did not retry once the cooldown elapsed: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("the retry made %d requests in total, want 2", got)
	}
	if build != 1 {
		t.Errorf("build ran %d times, want 1", build)
	}

	// Once it has succeeded nothing asks again.
	clock = clock.Add(time.Hour)
	if err := run(); err != nil {
		t.Fatalf("a call after a successful discovery failed: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("a call after success made %d requests in total, want 2", got)
	}
	if build != 1 {
		t.Errorf("build ran %d times after a second successful call, want 1", build)
	}
}

// TestDiscoveryOutlivesTheCallerThatStartedIt pins that a caller who
// abandons its request does not decide whether every later sign-in has a
// provider: discovery is shared, so it runs on its own context.
func TestDiscoveryOutlivesTheCallerThatStartedIt(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, func() (int, bool) { return http.StatusOK, true })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var d Discovery
	if err := d.Do(ctx, issuer, func(*oidc.Provider) {}); err != nil {
		t.Fatalf("discovery failed on an already-cancelled caller context: %v", err)
	}
}

// newIssuer starts a server that answers the OIDC discovery document.
// respond decides the status of each request and whether the body is the
// document, so a test can make the issuer fail and then recover.
func newIssuer(t *testing.T, respond func() (status int, ok bool)) string {
	t.Helper()

	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, ok := respond()
		if !ok {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{
			"issuer": "` + base + `",
			"authorization_endpoint": "` + base + `/authorize",
			"token_endpoint": "` + base + `/token",
			"jwks_uri": "` + base + `/keys"
		}`))
	}))
	base = srv.URL
	t.Cleanup(srv.Close)
	return base
}
