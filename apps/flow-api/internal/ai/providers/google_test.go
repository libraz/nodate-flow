package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGoogleProviderKeyNotInURL asserts the Gemini provider sends its API
// key via the x-goog-api-key header and never places it in the request
// URL. Keeping the key out of the URL means it cannot leak through a
// url.Error message, transport log, or proxy access log.
func TestGoogleProviderKeyNotInURL(t *testing.T) {
	t.Parallel()

	const wantKey = "sealed-key" // fakeDecryptor returns the sealed blob verbatim.
	var gotURL, gotRawQuery, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotRawQuery = r.URL.RawQuery
		gotHeader = r.Header.Get(googleAPIKeyHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`)
	}))
	t.Cleanup(srv.Close)

	p := newProviderForTest(t, KindGoogle, srv.URL)
	resp, err := p.Complete(context.Background(), Request{Prompt: "hello", Model: "m"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "hi" {
		t.Fatalf("response text = %q, want %q", resp.Text, "hi")
	}
	if gotHeader != wantKey {
		t.Fatalf("x-goog-api-key header = %q, want %q", gotHeader, wantKey)
	}
	if strings.Contains(gotRawQuery, "key=") || strings.Contains(gotRawQuery, wantKey) {
		t.Fatalf("request URL carried the key in the query: raw=%q full=%q", gotRawQuery, gotURL)
	}
	if strings.Contains(gotURL, wantKey) {
		t.Fatalf("request URL leaked the key: %q", gotURL)
	}
}

// TestGoogleProviderRejectsInvalidBaseURL asserts a malformed configured
// base URL fails fast at construction with ErrInvalidBaseURL rather than
// surfacing as an opaque transport error mid-call.
func TestGoogleProviderRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	cases := []string{
		"not a url",
		"ftp://example.com/v1",
		"//example.com/v1",
		"https://",
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := New(Config{
				Kind:         KindGoogle,
				Name:         "g",
				BaseURL:      raw,
				EncryptedKey: []byte("sealed-key"),
			}, fakeDecryptor{})
			if !errors.Is(err, ErrInvalidBaseURL) {
				t.Fatalf("New(base=%q) err = %v, want ErrInvalidBaseURL", raw, err)
			}
		})
	}
}
