package eventbus

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
)

// captureLogs redirects the default slog logger into a buffer for the
// duration of the test. Tests using it must stay sequential (no
// t.Parallel) because the default logger is process-global; Go never
// runs a sequential test alongside a parallel one in the same package.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestAppendBestEffortRecordsWhatWasLost pins the contract that makes
// the best-effort form acceptable at all: the row never reached the log,
// so the caller's identity and the payload have to survive somewhere.
// A bare `_ = Append(...)` leaves neither, which is why the static guard
// forbids it.
func TestAppendBestEffortRecordsWhatWasLost(t *testing.T) {
	buf := captureLogs(t)
	db, fail := failingStubDB(t, errors.New("connection reset"))

	taskID := int64(77)
	AppendBestEffort(context.Background(), dbretry.AutoCommit(db), Event{
		Type:        TaskCreated,
		WorkspaceID: 42,
		TaskID:      &taskID,
		Payload:     map[string]any{"taskId": "01HX-abc", "title": "Recoverable"},
	}, "mcp.create_task")

	if fail.count() == 0 {
		t.Fatal("AppendBestEffort must attempt the insert")
	}
	logged := buf.String()
	for _, want := range []string{
		"mcp.create_task",   // which flow lost the row
		"task.created",      // what kind of event it was
		"01HX-abc",          // the payload, so the row can be reconstructed
		"connection reset",  // why it failed
		`"task_id":77`,      // what it referred to
		`"workspace_id":42`, // and where
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("dropped-event log must carry %q; got:\n%s", want, logged)
		}
	}
}

// TestAppendReturnsTheFailure is the counterpart: the checked form must
// keep handing the error back so call sites that can propagate do.
func TestAppendReturnsTheFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("insert rejected")
	db, _ := failingStubDB(t, sentinel)

	err := Append(context.Background(), dbretry.AutoCommit(db), Event{Type: TaskCreated, WorkspaceID: 1})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Append must return the underlying failure, got %v", err)
	}
}

// TestAppendReverseEventReportsAlreadyReversed covers the concurrent
// double-reverse race at the source. Two requests can pass the handler's
// WasReversed pre-check, and the UNIQUE (workspace_id, reverses_event_id)
// index rejects the loser. Nothing is lost — the winner wrote the one
// compensating row that should exist — so the loser gets a condition it
// can answer with, and the failure is not logged as an append error.
func TestAppendReverseEventReportsAlreadyReversed(t *testing.T) {
	buf := captureLogs(t)
	dup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '209-763' for key 'events.uniq_events_reverses'"}
	db, _ := failingStubDB(t, dup)

	actor := int64(5)
	origin := int64(763)
	_, err := AppendReverseEvent(context.Background(), dbretry.AutoCommit(db), Event{
		Type:            "ai.agent.run.completed",
		WorkspaceID:     209,
		ActorUserID:     &actor,
		ReversesEventID: &origin,
	})
	if !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("a duplicate compensating row must report ErrAlreadyReversed, got %v", err)
	}
	// The driver error stays reachable for callers that inspect it.
	var me *mysql.MySQLError
	if !errors.As(err, &me) {
		t.Fatalf("the driver error must remain unwrappable, got %v", err)
	}
	logged := buf.String()
	if strings.Contains(logged, "append failed") {
		t.Errorf("a resolved reverse race must not be logged as an append failure:\n%s", logged)
	}
	if !strings.Contains(logged, `"level":"INFO"`) {
		t.Errorf("the resolved race should still be visible at info level:\n%s", logged)
	}
}

// TestAppendKeepsErrorLevelForRealFailures makes sure the branch above
// only quiets the reverse race: an ordinary duplicate (public_id
// collision, say) is still a genuine lost row.
func TestAppendKeepsErrorLevelForRealFailures(t *testing.T) {
	buf := captureLogs(t)
	dup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry for key 'events.uniq_events_public_id'"}
	db, _ := failingStubDB(t, dup)

	if err := Append(context.Background(), dbretry.AutoCommit(db), Event{Type: TaskCreated, WorkspaceID: 1}); err == nil {
		t.Fatal("Append must fail when the insert does")
	}
	if !strings.Contains(buf.String(), "append failed") {
		t.Errorf("a non-reverse failure must still be logged as an append failure:\n%s", buf.String())
	}
}
