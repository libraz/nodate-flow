package obs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddleware_ResponseControllerFlush(t *testing.T) {
	var flushErr error
	h := TraceMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if flushErr != nil {
		t.Fatalf("Flush() = %v, want nil", flushErr)
	}
	if !rec.Flushed {
		t.Fatal("underlying recorder was not flushed")
	}
}

func TestDomainForRoute(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/workspaces/{wsId}/calendars", "calendar"},
		{"/workspaces/{wsId}/calendars/{calId}/events", "calendar"},
		{"/share/cal/{token}", "calendar"},
		{"/invites/{token}/info", "calendar"},
		{"/me/invites", "calendar"},
		{"/workspaces/{wsId}/tasks", "task"},
		{"/health", "task"},
	}
	for _, c := range cases {
		if got := domainForRoute(c.in); got != c.want {
			t.Errorf("domainForRoute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
