package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// TestDatasetQueryFailed_LogsTheDriverError covers what the caller is
// deliberately not told.
//
// The error handed back names nothing about the database, because an
// export failure must not describe the store to whoever asked for the
// file. That makes the log the only remaining record: without it a
// transport-level regression collapses into a generic 500 with nothing
// for anyone to triage from.
//
// This used to live in the CSV route's own error writer. Both routes
// now return their errors for Huma to render, so the logging moved to
// the fetch path they share — which also means the JSON route logs it
// for the first time.
func TestDatasetQueryFailed_LogsTheDriverError(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	err := datasetQueryFailed(context.Background(), "lens", errors.New("synthetic upstream failure"))

	var problem *handlerutil.ProblemDetails
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T; want *handlerutil.ProblemDetails", err)
	}
	if problem.Type != apierrors.ExportTaskDatasetQueryFailed.Code {
		t.Fatalf("type = %q; want %q", problem.Type, apierrors.ExportTaskDatasetQueryFailed.Code)
	}
	if strings.Contains(problem.Detail, "synthetic upstream failure") {
		t.Fatalf("the caller must not be told what the store said, got detail %q", problem.Detail)
	}

	if buf.Len() == 0 {
		t.Fatal("the driver error was neither returned nor logged, so it is simply gone")
	}
	var entry map[string]any
	if uerr := json.Unmarshal(buf.Bytes(), &entry); uerr != nil {
		t.Fatalf("log output is not JSON: %v\n%s", uerr, buf.String())
	}
	if got, _ := entry["source"].(string); got != "lens" {
		t.Fatalf("log source = %q; want %q", got, "lens")
	}
	if got, _ := entry["error"].(string); !strings.Contains(got, "synthetic upstream failure") {
		t.Fatalf("log error attr = %q; want containing %q", got, "synthetic upstream failure")
	}
}
