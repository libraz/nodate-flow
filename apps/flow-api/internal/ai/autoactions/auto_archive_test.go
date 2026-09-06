package autoactions

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// The auto-archive write is a claim, not a patch: its predicate carries
// `archived_at IS NULL`, so a task archived by anyone else between the
// scan that selected it and the write matches zero rows. What follows the
// write — the task.archived event and the applied log line — describes an
// archive, and neither is true of a pass that archived nothing. An event
// appended there is an archive entry on the task's timeline with no
// archive behind it, added again on every pass the rule keeps matching.
//
// The two tests below drive the same task and the same action through the
// same code, differing only in what the driver reports as the affected-row
// count. That pairing is the whole evidence: the absence of the event in
// the zero-row case says nothing on its own, and only means something
// because the one-row case shows the event is produced from these inputs.
//
// A stub database/sql driver stands in for MySQL. The count is the one
// thing the test has to control, and it is exactly what a driver decides.

// archiveStub is the state one stub-backed pool records: the statements
// that reached it, how many rows the archive UPDATE reports, and how its
// transactions ended.
type archiveStub struct {
	mu sync.Mutex
	// archivedRows is what the tasks UPDATE reports as affected. Zero is
	// the task having been archived before this pass reached it.
	archivedRows int64
	statements   []string
	commits      int
	rollbacks    int
}

// answer records a statement and reports the result the stub gives it.
func (s *archiveStub) answer(query string) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, query)
	if strings.Contains(query, "UPDATE tasks SET archived_at") {
		return archiveResult{rows: s.archivedRows}, nil
	}
	// Everything else is the event INSERT, which needs a last insert id
	// for the append path to carry on.
	return archiveResult{rows: 1, lastID: int64(len(s.statements))}, nil
}

// issued counts the statements containing needle.
func (s *archiveStub) issued(needle string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, stmt := range s.statements {
		if strings.Contains(stmt, needle) {
			n++
		}
	}
	return n
}

// outcomes returns how many transactions committed and how many rolled
// back, so a skipped event can be told apart from an abandoned pass.
func (s *archiveStub) outcomes() (commits, rollbacks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits, s.rollbacks
}

type archiveResult struct {
	rows   int64
	lastID int64
}

func (r archiveResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r archiveResult) RowsAffected() (int64, error) { return r.rows, nil }

type (
	archiveDriver struct{}
	archiveConn   struct{ stub *archiveStub }
	archiveTx     struct{ stub *archiveStub }
)

var (
	archiveStubsMu sync.Mutex
	archiveStubs   = map[string]*archiveStub{}
)

func (archiveDriver) Open(dsn string) (driver.Conn, error) {
	archiveStubsMu.Lock()
	defer archiveStubsMu.Unlock()
	stub, ok := archiveStubs[dsn]
	if !ok {
		return nil, fmt.Errorf("autoactions: no stub registered for dsn %q", dsn)
	}
	return archiveConn{stub: stub}, nil
}

// Prepare is never reached: the connection answers statements directly,
// so database/sql has no reason to fall back to a prepared statement. It
// fails loudly rather than answering, because a statement that took the
// prepare path would not be recorded and every assertion below would then
// hold vacuously.
func (c archiveConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("autoactions: the stub answers statements directly")
}

func (c archiveConn) Close() error { return nil }

func (c archiveConn) Begin() (driver.Tx, error) { return archiveTx(c), nil }

func (c archiveConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	return c.stub.answer(query)
}

// CheckNamedValue accepts the argument types the archive path binds
// (uint32 ids, types.PublicID, sql.NullInt32, json.RawMessage) without
// conversion. The stub reads the SQL, not the arguments.
func (c archiveConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (t archiveTx) Commit() error {
	t.stub.mu.Lock()
	defer t.stub.mu.Unlock()
	t.stub.commits++
	return nil
}

func (t archiveTx) Rollback() error {
	t.stub.mu.Lock()
	defer t.stub.mu.Unlock()
	t.stub.rollbacks++
	return nil
}

// messageLog collects the messages an executor logs, so a line claiming
// an action was applied can be asserted on directly.
type messageLog struct {
	mu       sync.Mutex
	messages []string
}

func (m *messageLog) Enabled(context.Context, slog.Level) bool { return true }

func (m *messageLog) Handle(_ context.Context, r slog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, r.Message)
	return nil
}

func (m *messageLog) WithAttrs([]slog.Attr) slog.Handler { return m }

func (m *messageLog) WithGroup(string) slog.Handler { return m }

func (m *messageLog) logged(message string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, got := range m.messages {
		if got == message {
			return true
		}
	}
	return false
}

// seen renders what was logged, for a failure that needs to say what the
// executor reported instead.
func (m *messageLog) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.messages))
	copy(out, m.messages)
	return out
}

var (
	registerArchiveDriverOnce sync.Once
	archiveDSNSeq             atomic.Int64
)

// newArchiveExecutor builds an executor over a stub pool whose archive
// UPDATE reports archivedRows.
func newArchiveExecutor(t *testing.T, archivedRows int64) (*Executor, *archiveStub, *messageLog) {
	t.Helper()
	registerArchiveDriverOnce.Do(func() { sql.Register("autoactions-archive-stub", archiveDriver{}) })

	// database/sql keeps a process-wide driver registry, so the per-test
	// state hangs off the DSN and each test takes its own.
	dsn := "archive-" + strconv.FormatInt(archiveDSNSeq.Add(1), 10)
	stub := &archiveStub{archivedRows: archivedRows}
	archiveStubsMu.Lock()
	archiveStubs[dsn] = stub
	archiveStubsMu.Unlock()
	t.Cleanup(func() {
		archiveStubsMu.Lock()
		delete(archiveStubs, dsn)
		archiveStubsMu.Unlock()
	})

	db, err := sql.Open("autoactions-archive-stub", dsn)
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logs := &messageLog{}
	return &Executor{DB: db, Logger: slog.New(logs)}, stub, logs
}

// archiveRow is the task both cases archive: completed, which is the
// state the rule matches on.
func archiveRow() taskRow {
	return taskRow{
		id:           42,
		publicID:     types.New(),
		workspaceID:  7,
		derivedState: string(StateDone),
	}
}

func archiveAction() *Action {
	return &Action{
		Kind:       KindAutoArchiveCompleted,
		Confidence: 0.9,
		Reason:     "completed and idle past the threshold",
	}
}

// TestAutoArchiveRecordsTheArchiveItPerformed is the positive control. It
// is what makes the absence asserted below evidence rather than a test
// that could never have seen an event in the first place.
func TestAutoArchiveRecordsTheArchiveItPerformed(t *testing.T) {
	t.Parallel()

	e, stub, logs := newArchiveExecutor(t, 1)
	e.autoArchive(context.Background(), archiveRow(), archiveAction())

	if got := stub.issued("UPDATE tasks SET archived_at"); got != 1 {
		t.Fatalf("the archive UPDATE ran %d times, want 1", got)
	}
	if got := stub.issued("INSERT INTO events"); got != 1 {
		t.Errorf("a task this pass archived must get one timeline event, got %d", got)
	}
	if !logs.logged("auto-action applied: auto-archive") {
		t.Errorf("an archive that happened must be reported as applied; logged %v", logs.seen())
	}
	commits, _ := stub.outcomes()
	if commits != 1 {
		t.Errorf("the transaction committed %d times, want 1", commits)
	}
}

// TestAutoArchiveSaysNothingWhenTheTaskWasAlreadyArchived is the case the
// count answers.
//
// Same task, same action, same statements — the write simply matched no
// row, because the task carried an archived_at before this pass reached
// it. Nothing here archived anything, so nothing may say it did.
func TestAutoArchiveSaysNothingWhenTheTaskWasAlreadyArchived(t *testing.T) {
	t.Parallel()

	e, stub, logs := newArchiveExecutor(t, 0)
	e.autoArchive(context.Background(), archiveRow(), archiveAction())

	if got := stub.issued("UPDATE tasks SET archived_at"); got != 1 {
		t.Fatalf("the archive UPDATE ran %d times, want 1; the pass must still attempt the claim", got)
	}
	if got := stub.issued("INSERT INTO events"); got != 0 {
		t.Errorf("a pass that archived nothing appended %d timeline events; each one is an "+
			"archive entry with no archive behind it, and the rule adds another on every pass", got)
	}
	if logs.logged("auto-action applied: auto-archive") {
		t.Errorf("a pass that archived nothing reported an applied archive; logged %v", logs.seen())
	}
	if !logs.logged("auto-action skipped: auto-archive (task already archived)") {
		t.Errorf("a pass with nothing to do must say so; logged %v", logs.seen())
	}

	// Zero rows is the state the action wanted, not a failure: the
	// transaction commits and nothing is reported as an error.
	if logs.logged("auto-action executor: auto-archive") {
		t.Errorf("an already-archived task was reported as a failure; logged %v", logs.seen())
	}
	commits, rollbacks := stub.outcomes()
	if commits != 1 {
		t.Errorf("the transaction committed %d times, want 1", commits)
	}
	if rollbacks != 0 {
		t.Errorf("the transaction rolled back %d times; skipping the event is not an abort", rollbacks)
	}
}
