package log

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogger_ResponseControllerFlush(t *testing.T) {
	var flushErr error
	h := RequestLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestRequestLogger_EmitsRequestLine(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewRedactHandler(base))

	var sawLogger bool
	var sawReqID string
	h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()) != nil {
			sawLogger = true
		}
		sawReqID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !sawLogger {
		t.Fatal("handler did not receive request-scoped logger")
	}
	if sawReqID == "" {
		t.Fatal("handler did not receive request_id")
	}
	out := buf.String()
	for _, needle := range []string{`"method":"GET"`, `"path":"/hello"`, `"status":418`, `"request_id"`, `"http_request"`} {
		if !strings.Contains(out, needle) {
			t.Fatalf("log line missing %s: %s", needle, out)
		}
	}
}
