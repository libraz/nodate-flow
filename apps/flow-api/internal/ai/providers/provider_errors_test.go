package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeDecryptor is a no-op Decryptor for tests: it returns the sealed blob
// verbatim as the "plaintext" key. The provider error-handling paths under
// test never inspect the key value, only that decryption succeeds.
type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(blob []byte) ([]byte, error) {
	out := make([]byte, len(blob))
	copy(out, blob)
	return out, nil
}

// statusServer returns an httptest server that always responds with the
// given status code, optional Retry-After header, and body.
func statusServer(t *testing.T, status int, retryAfter, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newProviderForTest builds a provider of the given kind pointed at baseURL.
func newProviderForTest(t *testing.T, kind Kind, baseURL string) Provider {
	t.Helper()
	cfg := Config{
		Kind:         kind,
		Name:         "test-" + string(kind),
		BaseURL:      baseURL,
		EncryptedKey: []byte("sealed-key"),
	}
	if kind == KindOllama {
		cfg.EncryptedKey = nil
	}
	p, err := New(cfg, fakeDecryptor{})
	if err != nil {
		t.Fatalf("New(%s): %v", kind, err)
	}
	return p
}

// classifiedKinds are the three provider kinds repaired by this change plus
// openai_compat (which shares the openAIProvider code path that already
// classified errors) as a regression guard. KindOpenAI is omitted because
// its constructor hardcodes the production base URL and cannot be pointed at
// a test server.
var classifiedKinds = []Kind{KindAnthropic, KindGoogle, KindOllama, KindOpenAICompat}

// TestProviderHTTPStatusClassification asserts that every classified
// provider maps upstream HTTP status codes to the correct sentinel error,
// surfacing Retry-After on 429 and never collapsing auth/rate-limit
// failures into the generic UPSTREAM_UNREACHABLE bucket.
func TestProviderHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		retryAfter   string
		body         string
		wantSentinel error
		wantStatus   int
		wantRetryAft string
	}{
		{
			name:         "401 -> auth rejected",
			status:       http.StatusUnauthorized,
			body:         `{"error":{"message":"invalid api key"}}`,
			wantSentinel: ErrUpstreamAuthRejected,
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:         "403 -> auth rejected",
			status:       http.StatusForbidden,
			body:         `{"error":{"message":"forbidden"}}`,
			wantSentinel: ErrUpstreamAuthRejected,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "429 -> rate limited with retry-after",
			status:       http.StatusTooManyRequests,
			retryAfter:   "1",
			body:         `{"error":{"message":"slow down"}}`,
			wantSentinel: ErrUpstreamRateLimited,
			wantStatus:   http.StatusTooManyRequests,
			wantRetryAft: "1",
		},
		{
			name:         "500 -> request rejected",
			status:       http.StatusInternalServerError,
			body:         `{"error":{"message":"boom"}}`,
			wantSentinel: ErrUpstreamRequestRejected,
			wantStatus:   http.StatusInternalServerError,
		},
		{
			name:         "400 -> request rejected",
			status:       http.StatusBadRequest,
			body:         `{"error":{"message":"bad"}}`,
			wantSentinel: ErrUpstreamRequestRejected,
			wantStatus:   http.StatusBadRequest,
		},
	}

	for _, kind := range classifiedKinds {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				srv := statusServer(t, tc.status, tc.retryAfter, tc.body)
				p := newProviderForTest(t, kind, srv.URL)

				_, err := p.Complete(context.Background(), Request{Prompt: "hi", Model: "m"})
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, tc.wantSentinel) {
					t.Fatalf("sentinel mismatch: got %v, want errors.Is %v", err, tc.wantSentinel)
				}
				var ue *UpstreamError
				if !errors.As(err, &ue) {
					t.Fatalf("expected *UpstreamError, got %T: %v", err, err)
				}
				if ue.Status != tc.wantStatus {
					t.Fatalf("status mismatch: got %d, want %d", ue.Status, tc.wantStatus)
				}
				if ue.RetryAfter != tc.wantRetryAft {
					t.Fatalf("retry-after mismatch: got %q, want %q", ue.RetryAfter, tc.wantRetryAft)
				}
			})
		}
	}
}

// TestProviderTransportErrorClassification asserts that a transport-layer
// failure (connection refused: server closed) maps to UPSTREAM_UNREACHABLE
// rather than a bare error or 502 collapse.
func TestProviderTransportErrorClassification(t *testing.T) {
	for _, kind := range classifiedKinds {
		t.Run(string(kind), func(t *testing.T) {
			// Start then immediately close a server to obtain an address
			// that refuses connections.
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			addr := srv.URL
			srv.Close()

			p := newProviderForTest(t, kind, addr)
			_, err := p.Complete(context.Background(), Request{Prompt: "hi", Model: "m"})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrUpstreamUnreachable) {
				t.Fatalf("expected ErrUpstreamUnreachable, got %v", err)
			}
		})
	}
}

// TestProviderTimeoutClassification asserts that a request which exceeds the
// context deadline maps to UPSTREAM_TIMEOUT (distinct from a generic
// network failure).
func TestProviderTimeoutClassification(t *testing.T) {
	for _, kind := range classifiedKinds {
		t.Run(string(kind), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(200 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			p := newProviderForTest(t, kind, srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			_, err := p.Complete(ctx, Request{Prompt: "hi", Model: "m"})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrUpstreamTimeout) {
				t.Fatalf("expected ErrUpstreamTimeout, got %v", err)
			}
		})
	}
}
