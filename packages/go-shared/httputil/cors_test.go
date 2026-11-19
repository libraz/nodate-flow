package httputil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildCORSAllowsIdempotencyKey(t *testing.T) {
	middleware := BuildCORS([]string{"https://app.example"}, false)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodOptions, "/events", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,idempotency-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "idempotency-key") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Idempotency-Key", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}
