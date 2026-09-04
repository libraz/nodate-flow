package audit

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// TestRecord_LostEntryIsCountedAndLoggedAtError holds the visibility of
// an audit write that fails.
//
// The recorder deliberately does not fail its caller — an audit backend
// problem must not become a service outage — so the request is answered
// 2xx and the entry is simply gone. That makes the counter and the
// error-level log the only evidence the action ever happened. A warning
// nobody reads, and no counter at all, is what makes the loss silent.
func TestRecord_LostEntryIsCountedAndLoggedAtError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workspaceID uint32
		table       string
	}{
		{name: "workspace scoped", workspaceID: 9, table: obs.AuditTableWorkspace},
		{name: "instance scoped", workspaceID: 0, table: obs.AuditTableInstance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			before := testutil.ToFloat64(obs.AuditWriteFailuresCounter(tc.table))

			rec := New(generated.New(errDBTX{err: errors.New("audit backend unreachable")}))
			rec.Record(context.Background(), Entry{
				Action:       "member.role.change",
				ActorID:      42,
				WorkspaceID:  tc.workspaceID,
				ResourceType: "membership",
			})

			if got := testutil.ToFloat64(obs.AuditWriteFailuresCounter(tc.table)); got != before+1 {
				t.Fatalf("nf_audit_write_failures_total{table=%q} = %v, want %v", tc.table, got, before+1)
			}

			line := logs.String()
			if !strings.Contains(line, "level=ERROR") {
				t.Fatalf("audit loss logged as %q, want error level", line)
			}
			if !strings.Contains(line, "member.role.change") {
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
	rec.Record(context.Background(), Entry{Action: "task.create", WorkspaceID: 3, Metadata: map[string]any{"k": "v"}})
}
