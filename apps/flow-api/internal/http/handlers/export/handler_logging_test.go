package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// TestWriteFetchError_LogsUnknownError covers Task #11 item A:
// when writeFetchError falls back to INTERNAL.UNEXPECTED because the
// upstream error is not a known shape, the original error must be
// logged with the source context BEFORE the response is written.
// Without this guarantee, a transport-layer regression silently
// collapses to a generic 500 with nothing on disk for ops to triage.
func TestWriteFetchError_LogsUnknownError(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	rec := httptest.NewRecorder()
	upstream := errors.New("synthetic upstream failure")
	writeFetchError(context.Background(), rec, "lens", upstream)

	if rec.Code != 500 {
		t.Fatalf("status = %d; want 500", rec.Code)
	}

	if buf.Len() == 0 {
		t.Fatal("writeFetchError did not log the upstream error")
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}

	if got, _ := entry["msg"].(string); !strings.Contains(got, "fetch error") {
		t.Fatalf("log msg = %q; want containing 'fetch error'", got)
	}
	if got, _ := entry["source"].(string); got != "lens" {
		t.Fatalf("log source = %q; want %q", got, "lens")
	}
	if got, _ := entry["error"].(string); !strings.Contains(got, "synthetic upstream failure") {
		t.Fatalf("log error attr = %q; want containing %q", got, "synthetic upstream failure")
	}
}

// TestWriteFetchError_KnownShapeDoesNotLog asserts the happy fallback:
// when the error is already an apierror-shaped value the response
// pipeline forwards code + status without the noise of an extra log
// line. Otherwise every domain 4xx would generate phantom error logs.
func TestWriteFetchError_KnownShapeDoesNotLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// A wrapped huma.ErrorModel is the "known shape" — writeFetchError
	// projects it onto the wire envelope without an additional log.
	specErr := &huma.ErrorModel{
		Type:   "WS.TASK.NOT_FOUND",
		Title:  "Not Found",
		Status: 404,
		Detail: "task not found",
	}
	rec := httptest.NewRecorder()
	writeFetchError(context.Background(), rec, "workspace", specErr)

	if rec.Code != 404 {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if buf.Len() != 0 {
		t.Fatalf("known-shape error should not log; got: %s", buf.String())
	}
}
