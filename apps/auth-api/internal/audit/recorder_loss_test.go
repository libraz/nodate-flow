package audit

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

// errDBTX fails every write, which is the shape of an audit backend
// problem: the row was built, the statement was issued, nothing landed.
type errDBTX struct{ err error }

func (e errDBTX) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, e.err
}
func (errDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) { return nil, nil }
func (errDBTX) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, nil
}
func (errDBTX) QueryRowContext(context.Context, string, ...interface{}) *sql.Row { return nil }

// captureLogs redirects the default logger for the duration of the test
// and returns the buffer it writes to. Not parallel-safe: the default
// logger is process-wide.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestRecord_LostEntryIsLoggedAtError holds the visibility of an audit
// write that fails.
//
// The recorder deliberately does not fail its caller — an audit backend
// problem must not become a service outage — so the request is answered
// 2xx and the entry is simply gone. This service exposes no metrics
// endpoint, so the error-level log line is the only evidence the action
// ever happened; a warning nobody reads is what makes the loss silent.
func TestRecord_LostEntryIsLoggedAtError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workspaceID uint32
		table       string
	}{
		{name: "workspace scoped", workspaceID: 9, table: auditTableWorkspace},
		{name: "instance scoped", workspaceID: 0, table: auditTableInstance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)

			rec := New(generated.New(errDBTX{err: errors.New("audit backend unreachable")}))
			rec.Record(context.Background(), Entry{
				Action:       "auth.mfa.disable",
				ActorID:      42,
				WorkspaceID:  tc.workspaceID,
				ResourceType: "user",
			})

			line := logs.String()
			if !strings.Contains(line, "level=ERROR") {
				t.Fatalf("audit loss logged as %q, want error level", line)
			}
			if !strings.Contains(line, "auth.mfa.disable") {
				t.Fatalf("audit loss log %q does not name the lost action", line)
			}
			if !strings.Contains(line, tc.table) {
				t.Fatalf("audit loss log %q does not name the destination table", line)
			}
		})
	}
}

// TestRecord_FailedWriteDoesNotFailTheCaller holds the other half of the
// decision: a failing backend is absorbed. Record has no error return to
// assert on, so what this pins is that the call completes rather than
// propagating the failure as a panic into the request path.
func TestRecord_FailedWriteDoesNotFailTheCaller(t *testing.T) {
	captureLogs(t)
	rec := New(generated.New(errDBTX{err: errors.New("audit backend unreachable")}))

	rec.Record(context.Background(), Entry{Action: "auth.login"})
	rec.Record(context.Background(), Entry{Action: "user.update", WorkspaceID: 3, Metadata: map[string]any{"k": "v"}})
}
