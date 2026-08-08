package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostMessageBoundsTheFailureBody pins the ceiling on what a failing
// Slack response may drag into this process.
//
// The body of a non-2xx reply is quoted verbatim into the returned
// error, which is then logged. Without a bound, the size of that log
// line — and of the buffer behind it — is chosen by whatever answered
// the request, and an MCP tool call is enough to trigger it.
func TestPostMessageBoundsTheFailureBody(t *testing.T) {
	const served = maxResponseBytes * 4

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", served)))
	}))
	defer srv.Close()

	c := &OutboundClient{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.PostMessage(context.Background(), "C1", "hi")
	if err == nil {
		t.Fatal("a 500 response must surface as an error")
	}
	if len(err.Error()) > maxResponseBytes+256 {
		t.Fatalf("the error quotes %d bytes of a %d byte response; the read is unbounded",
			len(err.Error()), served)
	}
}

// TestPostMessageRoundTrip is the happy path this package had no test
// for, so the bound above cannot be satisfied by a client that fails
// every call.
func TestPostMessageRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0k3n" {
			t.Errorf("missing/incorrect auth header")
		}
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000000.000100"}`))
	}))
	defer srv.Close()

	c := &OutboundClient{Token: "t0k3n", BaseURL: srv.URL, HTTP: srv.Client()}
	ts, err := c.PostMessage(context.Background(), "C1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if ts != "1700000000.000100" {
		t.Fatalf("expected the message ts, got %q", ts)
	}
}
