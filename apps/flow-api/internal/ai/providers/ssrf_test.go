package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// strictPolicy clears the escape hatch so the test sees the policy a
// production deployment runs under. It must not be combined with
// t.Parallel: t.Setenv is process-global.
func strictPolicy(t *testing.T) {
	t.Helper()
	t.Setenv(AllowPrivateEnv, "")
}

// TestBaseURLRejectsInternalDestinations covers the addresses a workspace
// admin could otherwise point a provider at to make the api process issue
// requests inside its own network — loopback, the cloud metadata service,
// private space, IPv6 loopback — plus the schemes that are not upstream
// endpoints at all.
//
// Every kind is checked, not just Google: the kinds that exist precisely
// to be pointed at a custom endpoint are the ones an attack would use.
func TestBaseURLRejectsInternalDestinations(t *testing.T) {
	strictPolicy(t)

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"loopback v4", "http://127.0.0.1:11434", ErrBaseURLDestinationNotAllowed},
		{"loopback v6", "http://[::1]:8080/v1", ErrBaseURLDestinationNotAllowed},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", ErrBaseURLDestinationNotAllowed},
		{"gcp metadata over https", "https://169.254.169.254/computeMetadata/v1/", ErrBaseURLDestinationNotAllowed},
		{"rfc1918 ten", "http://10.0.0.5:8000/v1", ErrBaseURLDestinationNotAllowed},
		{"rfc1918 one-nine-two", "https://192.168.1.10/v1", ErrBaseURLDestinationNotAllowed},
		{"rfc1918 one-seven-two", "http://172.16.4.4/v1", ErrBaseURLDestinationNotAllowed},
		{"unique local v6", "http://[fd00::1]/v1", ErrBaseURLDestinationNotAllowed},
		{"unspecified", "http://0.0.0.0:9000/v1", ErrBaseURLDestinationNotAllowed},
		{"carrier grade nat", "http://100.64.0.1/v1", ErrBaseURLDestinationNotAllowed},
		{"file scheme", "file:///etc/passwd", ErrInvalidBaseURL},
		{"gopher scheme", "gopher://127.0.0.1:70/", ErrInvalidBaseURL},
		{"no scheme at all", "//127.0.0.1/v1", ErrInvalidBaseURL},
		{"embedded credentials", "https://user:pass@api.example.com/v1", ErrInvalidBaseURL},
	}

	kinds := []Kind{KindAnthropic, KindOllama, KindOpenAICompat, KindGoogle}
	for _, tc := range cases {
		for _, kind := range kinds {
			cfg := Config{Kind: kind, Name: "p", BaseURL: tc.raw, EncryptedKey: []byte("sealed")}
			if err := Validate(cfg); !errors.Is(err, tc.want) {
				t.Errorf("Validate(kind=%s, base=%s [%s]) err = %v, want %v", kind, tc.raw, tc.name, err, tc.want)
			}
			if _, err := New(cfg, fakeDecryptor{}); !errors.Is(err, tc.want) {
				t.Errorf("New(kind=%s, base=%s [%s]) err = %v, want %v", kind, tc.raw, tc.name, err, tc.want)
			}
		}
	}
}

// TestBaseURLAcceptsPublicDestinations guards the other direction: the
// policy must not reject the ordinary case, or every hosted provider stops
// working the moment the guard lands.
func TestBaseURLAcceptsPublicDestinations(t *testing.T) {
	strictPolicy(t)

	for _, raw := range []string{
		"",
		"https://api.anthropic.com",
		"https://generativelanguage.googleapis.com/v1beta/models",
		"https://8.8.8.8/v1",
		"http://[2001:4860:4860::8888]/v1",
	} {
		if err := validateBaseURL(raw); err != nil {
			t.Errorf("validateBaseURL(%q) = %v, want nil", raw, err)
		}
	}
}

// TestBaseURLDestinationRejectsNameResolvingInternally covers the case a
// literal-address check cannot: a hostname. "localhost" is resolved by the
// system resolver, so the rejection comes from the resolved address rather
// than from the spelling of the host.
func TestBaseURLDestinationRejectsNameResolvingInternally(t *testing.T) {
	strictPolicy(t)

	err := ValidateBaseURLDestination(context.Background(), "http://localhost:11434/v1")
	if !errors.Is(err, ErrBaseURLDestinationNotAllowed) {
		t.Fatalf("ValidateBaseURLDestination(localhost) = %v, want ErrBaseURLDestinationNotAllowed", err)
	}
	// The rejection has to name the host it refused, or an admin reading
	// the log cannot tell which field was the problem.
	if !strings.Contains(err.Error(), "localhost") {
		t.Errorf("error %q does not name the rejected host", err)
	}

	// "127.1" is a loopback address that net.ParseIP does not recognise, so
	// the literal-address check in validateBaseURL lets it through. It is
	// the resolver that has the last word, which is the reason the
	// submit-time gate resolves rather than pattern-matching the host.
	if err := ValidateBaseURLDestination(context.Background(), "http://127.1:8080/v1"); !errors.Is(err, ErrBaseURLDestinationNotAllowed) {
		t.Errorf("ValidateBaseURLDestination(127.1) = %v, want ErrBaseURLDestinationNotAllowed", err)
	}
}

// TestSafeControlRejectsInternalConnects exercises the dial-time hook
// directly. It is the check that survives a DNS record changing between
// validation and the call, so it has to hold on the address alone, with no
// URL in sight.
func TestSafeControlRejectsInternalConnects(t *testing.T) {
	strictPolicy(t)

	blocked := []string{
		"127.0.0.1:11434",
		"[::1]:8080",
		"169.254.169.254:80",
		"10.1.2.3:443",
		"192.168.0.1:80",
		"172.20.0.1:80",
	}
	for _, addr := range blocked {
		if err := safeControl("tcp4", addr, nil); !errors.Is(err, ErrBaseURLDestinationNotAllowed) {
			t.Errorf("safeControl(%s) = %v, want ErrBaseURLDestinationNotAllowed", addr, err)
		}
	}
	if err := safeControl("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("safeControl(public address) = %v, want nil", err)
	}
	// A non-TCP network is never an upstream LLM call.
	if err := safeControl("unix", "/var/run/anything.sock", nil); !errors.Is(err, ErrBaseURLDestinationNotAllowed) {
		t.Errorf("safeControl(unix) = %v, want ErrBaseURLDestinationNotAllowed", err)
	}
}

// TestCompleteCannotReachLoopbackServer is the end-to-end statement: a
// provider row configured with an internal endpoint produces no request to
// that endpoint. The server records whether it was hit, so the assertion
// is about the traffic rather than about which error came back.
func TestCompleteCannotReachLoopbackServer(t *testing.T) {
	strictPolicy(t)

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"internal service response"}`))
	}))
	defer srv.Close()

	// Construction is refused outright for a literal loopback address, so
	// the completion path is reached through the host form the connect-time
	// guard owns: a name that resolves to the same place.
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split %s: %v", srv.URL, err)
	}
	host := "http://localhost:" + port

	p, err := New(Config{
		Kind:         KindOpenAICompat,
		Name:         "compat",
		BaseURL:      host,
		EncryptedKey: []byte("sealed"),
	}, fakeDecryptor{})
	if err != nil {
		t.Fatalf("New(kind=openai_compat, base=%s): %v", host, err)
	}

	_, err = p.Complete(context.Background(), Request{Model: "m", Prompt: "hello"})
	if err == nil {
		t.Fatal("Complete reached the loopback endpoint, want a refused connect")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("loopback server received %d requests, want 0", n)
	}
	// The reason has to survive the transport's error chain. Reported as a
	// generic unreachable upstream, this refusal reads as a network fault
	// and an operator can spend a long time looking at the network for a
	// connection this process declined to make.
	if !errors.Is(err, ErrBaseURLDestinationNotAllowed) {
		t.Fatalf("Complete err = %v, want it to carry ErrBaseURLDestinationNotAllowed", err)
	}
	if errors.Is(err, ErrUpstreamUnreachable) {
		t.Fatalf("Complete err = %v, want it not to be reported as an unreachable upstream", err)
	}
}

// TestClassifyTransportErrorKeepsTheRefusalReason checks the same
// mapping directly, including through the wrapping the http client adds
// around a dialer error.
func TestClassifyTransportErrorKeepsTheRefusalReason(t *testing.T) {
	t.Parallel()

	wrapped := &url.Error{
		Op:  "Post",
		URL: "http://localhost:11434/v1/chat/completions",
		Err: fmt.Errorf("dial tcp: %w", ErrBaseURLDestinationNotAllowed),
	}
	got := classifyTransportError(context.Background(), wrapped)
	if !errors.Is(got, ErrBaseURLDestinationNotAllowed) {
		t.Fatalf("classifyTransportError = %v, want ErrBaseURLDestinationNotAllowed", got)
	}

	// An ordinary connect failure keeps its existing classification.
	plain := &url.Error{Op: "Post", URL: "https://api.example.com", Err: errors.New("connection refused")}
	if got := classifyTransportError(context.Background(), plain); !errors.Is(got, ErrUpstreamUnreachable) {
		t.Fatalf("classifyTransportError(connection refused) = %v, want ErrUpstreamUnreachable", got)
	}
}

// TestAllowPrivateEscapeHatch pins the operator opt-out that local
// inference depends on: with it set, a loopback endpoint is configurable
// and reachable again.
func TestAllowPrivateEscapeHatch(t *testing.T) {
	t.Setenv(AllowPrivateEnv, "1")

	if err := validateBaseURL("http://127.0.0.1:11434"); err != nil {
		t.Fatalf("validateBaseURL under the escape hatch = %v, want nil", err)
	}
	if err := safeControl("tcp4", "127.0.0.1:11434", nil); err != nil {
		t.Fatalf("safeControl under the escape hatch = %v, want nil", err)
	}
	if err := ValidateBaseURLDestination(context.Background(), "http://localhost:11434"); err != nil {
		t.Fatalf("ValidateBaseURLDestination under the escape hatch = %v, want nil", err)
	}
}

// TestIsDisallowedIPRejectsUnparseable states the default for the case the
// dial hook cannot make sense of: no address, no connection.
func TestIsDisallowedIPRejectsUnparseable(t *testing.T) {
	t.Parallel()

	if !isDisallowedIP(nil) {
		t.Fatal("isDisallowedIP(nil) = false, want true")
	}
	if !isDisallowedIP(net.ParseIP("not-an-ip")) {
		t.Fatal("isDisallowedIP(unparseable) = false, want true")
	}
}
