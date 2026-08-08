package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateIssueCommentBoundsTheFailureBody pins the ceiling on what a
// failing GitHub response may drag into this process.
//
// The body of a non-2xx reply is quoted verbatim into the returned
// error, which is then logged. Without a bound, the size of that log
// line — and of the buffer behind it — is chosen by whatever answered
// the request, and an MCP tool call is enough to trigger it.
func TestCreateIssueCommentBoundsTheFailureBody(t *testing.T) {
	const served = maxResponseBytes * 4

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", served)))
	}))
	defer srv.Close()

	c := &OutboundClient{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.CreateIssueComment(context.Background(), "o", "r", 1, "hi")
	if err == nil {
		t.Fatal("a 500 response must surface as an error")
	}
	if len(err.Error()) > maxResponseBytes+256 {
		t.Fatalf("the error quotes %d bytes of a %d byte response; the read is unbounded",
			len(err.Error()), served)
	}
}
