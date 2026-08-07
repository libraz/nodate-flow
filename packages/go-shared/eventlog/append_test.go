package eventlog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// None of the tests in this file call t.Parallel. The subscriber
// registry is process-wide, so a test that counts dispatches would
// otherwise count appends made by whatever else was running at the same
// time — and one that appends would fire another test's subscriber.
// This package is small; serial is the honest way to hold that global.

// TestAppendWritesTheRow pins that an append reaches the database as one
// INSERT into events, carrying every field it was given.
//
// State is derived from this log, so a row that is not written is not a
// missing audit line, it is a wrong state that nothing later corrects.
func TestAppendWritesTheRow(t *testing.T) {
	db, rec := stubDB(t)

	actor := uint32(7)
	task := uint32(99)
	occurred := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	pubID, err := Append(context.Background(), db, Event{
		Type:        "item.scheduled",
		WorkspaceID: 3,
		ActorUserID: &actor,
		TaskID:      &task,
		Payload:     map[string]any{"note": "hello"},
		OccurredAt:  occurred,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if pubID == (dbtype.PublicID{}) {
		t.Fatal("append returned the zero public id")
	}

	stmts := rec.statements()
	if len(stmts) != 1 {
		t.Fatalf("statement count = %d, want 1", len(stmts))
	}
	got := stmts[0]
	if !strings.Contains(got.query, "INSERT INTO events") {
		t.Fatalf("query = %q, want an INSERT INTO events", got.query)
	}
	if len(got.args) != 7 {
		t.Fatalf("arg count = %d, want 7", len(got.args))
	}
	if got.args[0] != driver.Value(pubID) {
		t.Errorf("public_id arg = %v, want the returned %v", got.args[0], pubID)
	}
	if got.args[1] != driver.Value(uint32(3)) {
		t.Errorf("workspace_id arg = %v, want 3", got.args[1])
	}
	if got.args[2] != driver.Value(task) {
		t.Errorf("task_id arg = %v, want %d", got.args[2], task)
	}
	if got.args[3] != driver.Value(actor) {
		t.Errorf("actor_user_id arg = %v, want %d", got.args[3], actor)
	}
	if got.args[4] != driver.Value("item.scheduled") {
		t.Errorf("type arg = %v, want item.scheduled", got.args[4])
	}
	raw, ok := got.args[5].(json.RawMessage)
	if !ok {
		t.Fatalf("payload arg is %T, want json.RawMessage", got.args[5])
	}
	if string(raw) != `{"note":"hello"}` {
		t.Errorf("payload arg = %s, want the marshalled payload", raw)
	}
	if got.args[6] != driver.Value(occurred) {
		t.Errorf("occurred_at arg = %v, want %v", got.args[6], occurred)
	}
}

// TestAppendDefaultsPayloadAndTimestamp covers the two fields Append
// fills in for the caller. An empty payload is stored as an empty JSON
// object rather than NULL, and a zero OccurredAt means now.
func TestAppendDefaultsPayloadAndTimestamp(t *testing.T) {
	db, rec := stubDB(t)

	before := time.Now().UTC()
	if _, err := Append(context.Background(), db, Event{Type: "member.joined", WorkspaceID: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	after := time.Now().UTC()

	args := rec.statements()[0].args
	raw, ok := args[5].(json.RawMessage)
	if !ok || string(raw) != "{}" {
		t.Errorf("payload arg = %v, want {}", args[5])
	}
	if args[2] != nil {
		t.Errorf("task_id arg = %v, want NULL for an event with no task", args[2])
	}
	if args[3] != nil {
		t.Errorf("actor_user_id arg = %v, want NULL for a system event", args[3])
	}
	occurred, ok := args[6].(time.Time)
	if !ok {
		t.Fatalf("occurred_at arg is %T, want time.Time", args[6])
	}
	if occurred.Before(before) || occurred.After(after) {
		t.Errorf("occurred_at = %v, want a timestamp taken during the call", occurred)
	}
}

// TestAppendPropagatesTheWriteError is the core of the append-only
// contract: a failed write must come back to the caller as an error.
//
// It is checked here, at the source, because the caller-side rule that
// the error may not be discarded is only worth anything if there is an
// error to discard. An Append that logged and returned nil would leave
// every one of those call sites correct and the log short a row.
func TestAppendPropagatesTheWriteError(t *testing.T) {
	db, rec := stubDB(t)
	boom := errors.New("events table is read-only")
	rec.failWith(boom)

	pubID, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3})
	if !errors.Is(err, boom) {
		t.Fatalf("append error = %v, want %v", err, boom)
	}
	if pubID != (dbtype.PublicID{}) {
		t.Errorf("public id = %v, want the zero value on failure", pubID)
	}
}

// TestAppendDoesNotFanOutOnFailure keeps a failed write from announcing
// itself. A subscriber woken for a row that was never inserted resolves
// it to nothing, and a webhook or notification derived from it describes
// an event that did not happen.
func TestAppendDoesNotFanOutOnFailure(t *testing.T) {
	db, rec := stubDB(t)
	rec.failWith(errors.New("write failed"))
	var sub subscriber
	sub.register(t)

	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err == nil {
		t.Fatal("append returned nil despite the write failing")
	}
	if got := sub.fired(); got != 0 {
		t.Fatalf("subscribers fired %d times for a row that was never written", got)
	}
}

// TestAppendRejectsInternalIDsInThePayload pins the rail that keeps
// auto-increment keys out of the timeline, and pins that it runs before
// the write: a rejected payload must leave no row behind.
func TestAppendRejectsInternalIDsInThePayload(t *testing.T) {
	db, rec := stubDB(t)

	_, err := Append(context.Background(), db, Event{
		Type:        "item.scheduled",
		WorkspaceID: 3,
		Payload:     map[string]any{"taskId": 42},
	})
	if err == nil {
		t.Fatal("append accepted a payload carrying an internal id")
	}
	if n := len(rec.statements()); n != 0 {
		t.Fatalf("statement count = %d, want 0 — the payload was rejected", n)
	}
}

// TestAppendRetriesDeadlockOnAutoCommit covers the *sql.DB half of the
// retry split. Parallel writers contend on FK record locks for shared
// parents, and InnoDB resolves that by rolling one side back;
// re-issuing the statement clears it.
func TestAppendRetriesDeadlockOnAutoCommit(t *testing.T) {
	db, rec := stubDB(t)
	rec.failWith(&mysql.MySQLError{Number: 1213, Message: "Deadlock found"})
	rec.recoverAfter(1)

	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if n := len(rec.statements()); n != 2 {
		t.Fatalf("statement count = %d, want 2 — the deadlocked statement is re-issued", n)
	}
}

// TestAppendDoesNotRetryInsideATransaction covers the other half. A
// deadlock invalidates the whole transaction, so re-issuing this one
// statement would send it to a transaction the server has already
// rolled back; the unit that has to be retried is the caller's
// transaction.
func TestAppendDoesNotRetryInsideATransaction(t *testing.T) {
	db, rec := stubDB(t)
	deadlock := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	rec.failWith(deadlock)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = Append(context.Background(), tx, Event{Type: "item.scheduled", WorkspaceID: 3})
	if !errors.Is(err, deadlock) {
		t.Fatalf("append error = %v, want the deadlock surfaced to the caller", err)
	}
	if n := len(rec.statements()); n != 1 {
		t.Fatalf("statement count = %d, want 1 — the transaction is the retry unit, not the statement", n)
	}
}

// TestAppendRetriesTheWholeTransactionThroughInTx is what the caller is
// supposed to do with the transaction case. auth-api's workspace-create
// and invite-accept paths reached the appender through a hand-rolled
// transaction, got no retry anywhere, and returned 500 for work that
// would have succeeded on a second attempt.
func TestAppendRetriesTheWholeTransactionThroughInTx(t *testing.T) {
	db, rec := stubDB(t)
	rec.failWith(&mysql.MySQLError{Number: 1213, Message: "Deadlock found"})
	rec.recoverAfter(1)

	err := dbretry.InTx(context.Background(), db, "eventlog.test", nil, func(ctx context.Context, tx *sql.Tx) error {
		_, aerr := Append(ctx, tx, Event{Type: "item.scheduled", WorkspaceID: 3})
		return aerr
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if n := len(rec.statements()); n != 2 {
		t.Fatalf("statement count = %d, want 2 — the transaction is retried as a unit", n)
	}
}

// TestFanOutDeferredUntilCommit pins that subscribers are woken after
// the transaction commits, not while it is open. They read the row on
// their own connection, where an uncommitted insert is not visible.
func TestFanOutDeferredUntilCommit(t *testing.T) {
	db, _ := stubDB(t)
	var sub subscriber
	sub.register(t)

	firedInside := false
	err := dbretry.InTx(context.Background(), db, "eventlog.test", nil, func(ctx context.Context, tx *sql.Tx) error {
		if _, aerr := Append(ctx, tx, Event{Type: "item.scheduled", WorkspaceID: 3}); aerr != nil {
			return aerr
		}
		firedInside = sub.fired() > 0
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if firedInside {
		t.Error("subscribers ran while the transaction was still open")
	}
	if got := sub.fired(); got != 1 {
		t.Fatalf("subscribers fired %d times after commit, want 1", got)
	}
}

// TestFanOutRefusedInHandRolledTransaction pins the refusal. A
// transaction the caller opened themselves has no commit boundary to
// defer to, so waking subscribers there hands each of them an id that
// resolves to nothing yet — every delivery quietly evaporates. The row
// is still written and its public id still returned; only the fan-out
// is withheld, and loudly.
func TestFanOutRefusedInHandRolledTransaction(t *testing.T) {
	db, _ := stubDB(t)
	var sub subscriber
	sub.register(t)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	pubID, err := Append(context.Background(), tx, Event{Type: "item.scheduled", WorkspaceID: 3})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if pubID == (dbtype.PublicID{}) {
		t.Error("the row was written, so its public id must still come back")
	}
	if got := sub.fired(); got != 0 {
		t.Fatalf("subscribers fired %d times without a commit boundary, want 0", got)
	}
}

// TestFanOutCarriesTheInsertedRow checks what a subscriber is actually
// handed. The internal id is the whole point: webhook deliveries dedupe
// on the event's public id and notifications anchor on
// source_event_id, so a subscriber that only learns the workspace and
// the type cannot deliver anything.
func TestFanOutCarriesTheInsertedRow(t *testing.T) {
	db, _ := stubDB(t)
	var sub subscriber
	sub.register(t)

	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}

	call := sub.last()
	if call.workspaceID != 3 {
		t.Errorf("workspace id = %d, want 3", call.workspaceID)
	}
	if call.eventType != "item.scheduled" {
		t.Errorf("event type = %q, want item.scheduled", call.eventType)
	}
	if call.eventInternalID != stubLastInsertID {
		t.Errorf("event id = %d, want the inserted row's id %d", call.eventInternalID, stubLastInsertID)
	}
	if call.seq <= 0 {
		t.Errorf("sequence = %d, want a positive number so subscribers can spot gaps", call.seq)
	}
}

// TestHooksFireInRegistrationOrderAndCanBeRemoved covers the registry
// itself: several subscribers each get the event, in the order they
// subscribed, and a removed one stops receiving.
//
// Removal is per-handle rather than a global clear because the bridge
// that forwards these appends to flow-api's subscribers is registered
// in the same list; a test that cleared it would take the product's
// fan-out down with its own.
func TestHooksFireInRegistrationOrderAndCanBeRemoved(t *testing.T) {
	db, _ := stubDB(t)

	var order []string
	first := RegisterHook(func(context.Context, uint32, string, uint64) {
		order = append(order, "first")
	})
	t.Cleanup(func() { RemoveHook(first) })
	second := RegisterHook(func(context.Context, uint32, string, uint64) {
		order = append(order, "second")
	})
	t.Cleanup(func() { RemoveHook(second) })

	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("dispatch order = %v, want [first second]", order)
	}

	RemoveHook(first)
	order = nil
	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(order) != 1 || order[0] != "second" {
		t.Fatalf("dispatch order after removal = %v, want [second]", order)
	}

	// A handle is good exactly once; removing again must not disturb
	// the subscribers that remain.
	RemoveHook(first)
	order = nil
	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(order) != 1 || order[0] != "second" {
		t.Fatalf("dispatch order after a repeated removal = %v, want [second]", order)
	}
}

// TestHandlesSurviveOtherRegistrations pins what the handle is worth. It
// used to be a slice position, which addressed a different subscriber as
// soon as an earlier one went away — so an unregister would have taken
// down whoever had shifted into that slot.
func TestHandlesSurviveOtherRegistrations(t *testing.T) {
	db, _ := stubDB(t)

	var fired []string
	a := RegisterHook(func(context.Context, uint32, string, uint64) { fired = append(fired, "a") })
	t.Cleanup(func() { RemoveHook(a) })
	b := RegisterHook(func(context.Context, uint32, string, uint64) { fired = append(fired, "b") })
	t.Cleanup(func() { RemoveHook(b) })
	c := RegisterHook(func(context.Context, uint32, string, uint64) { fired = append(fired, "c") })
	t.Cleanup(func() { RemoveHook(c) })

	RemoveHook(a)
	RemoveHook(b)

	if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(fired) != 1 || fired[0] != "c" {
		t.Fatalf("subscribers fired = %v, want only the one that was never removed", fired)
	}
}

// TestSeqIncrementsPerDispatch checks the gap counter subscribers relay
// to clients: two appends must not carry the same sequence.
func TestSeqIncrementsPerDispatch(t *testing.T) {
	db, _ := stubDB(t)
	var sub subscriber
	sub.register(t)

	for range 2 {
		if _, err := Append(context.Background(), db, Event{Type: "item.scheduled", WorkspaceID: 3}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	calls := sub.calls()
	if len(calls) != 2 {
		t.Fatalf("dispatches = %d, want 2", len(calls))
	}
	if calls[1].seq <= calls[0].seq {
		t.Fatalf("sequence went %d then %d, want strictly increasing", calls[0].seq, calls[1].seq)
	}
}

// TestSeqFromContextWithoutTag returns zero rather than panicking, so a
// subscriber invoked outside a dispatch reads a well-defined value.
func TestSeqFromContextWithoutTag(t *testing.T) {
	if got := SeqFromContext(context.Background()); got != 0 {
		t.Fatalf("SeqFromContext on an untagged context = %d, want 0", got)
	}
	//nolint:staticcheck // SA1012: a nil context is exactly the case under test.
	if got := SeqFromContext(nil); got != 0 {
		t.Fatalf("SeqFromContext(nil) = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------
// subscriber
// ---------------------------------------------------------------------

type dispatch struct {
	workspaceID     uint32
	eventType       string
	eventInternalID uint64
	seq             int64
}

// subscriber is a NotifyHook that records what it was handed.
type subscriber struct {
	mu   sync.Mutex
	seen []dispatch
}

func (s *subscriber) register(t *testing.T) {
	t.Helper()
	handle := RegisterHook(func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.seen = append(s.seen, dispatch{
			workspaceID:     workspaceID,
			eventType:       eventType,
			eventInternalID: eventInternalID,
			seq:             SeqFromContext(ctx),
		})
	})
	t.Cleanup(func() { RemoveHook(handle) })
}

func (s *subscriber) calls() []dispatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]dispatch(nil), s.seen...)
}

func (s *subscriber) fired() int { return len(s.calls()) }

func (s *subscriber) last() dispatch {
	c := s.calls()
	if len(c) == 0 {
		return dispatch{}
	}
	return c[len(c)-1]
}

// ---------------------------------------------------------------------
// stub driver
// ---------------------------------------------------------------------

// stubLastInsertID is the events.id the stub reports for every insert.
const stubLastInsertID = 4242

// statement is one query the appender sent to the database.
type statement struct {
	query string
	args  []driver.Value
}

// recorder is the programmable database behind a stub *sql.DB: it
// records the statements it is sent and can be told to fail, so the
// tests can drive the write path without a server.
type recorder struct {
	mu        sync.Mutex
	seen      []statement
	err       error
	failsLeft int
}

func (r *recorder) statements() []statement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]statement(nil), r.seen...)
}

// failWith makes every statement fail with err.
func (r *recorder) failWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
	r.failsLeft = -1 // until told otherwise
}

// recoverAfter limits the failure to the first n statements.
func (r *recorder) recoverAfter(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failsLeft = n
}

func (r *recorder) record(query string, args []driver.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, statement{query: query, args: args})
	if r.err == nil || r.failsLeft == 0 {
		return nil
	}
	if r.failsLeft > 0 {
		r.failsLeft--
	}
	return r.err
}

type (
	stubDriver struct{}
	stubConn   struct{ rec *recorder }
	stubStmt   struct{}
	stubTx     struct{}
	stubRows   struct{}
	stubResult struct{}
)

// recorders routes each sql.Open to the recorder its DSN names, so
// tests get independent databases out of one registered driver.
var (
	openMu     sync.Mutex
	openSeq    int
	recorders  = map[string]*recorder{}
	registerDB sync.Once
)

func (stubDriver) Open(dsn string) (driver.Conn, error) {
	openMu.Lock()
	defer openMu.Unlock()
	rec, ok := recorders[dsn]
	if !ok {
		return nil, errors.New("eventlog stub: unknown dsn " + dsn)
	}
	return stubConn{rec: rec}, nil
}

func (stubConn) Prepare(string) (driver.Stmt, error) { return stubStmt{}, nil }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return stubTx{}, nil }

// ExecContext keeps database/sql off the Prepare path, so the stub does
// not have to model placeholders.
func (c stubConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	vals := make([]driver.Value, 0, len(args))
	for _, a := range args {
		vals = append(vals, a.Value)
	}
	if err := c.rec.record(query, vals); err != nil {
		return nil, err
	}
	return stubResult{}, nil
}

// CheckNamedValue takes the arguments through unconverted so the tests
// can assert on the values the appender actually passed.
func (stubConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (stubStmt) Close() error                               { return nil }
func (stubStmt) NumInput() int                              { return -1 }
func (stubStmt) Exec([]driver.Value) (driver.Result, error) { return stubResult{}, nil }
func (stubStmt) Query([]driver.Value) (driver.Rows, error)  { return stubRows{}, nil }

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

func (stubRows) Columns() []string              { return nil }
func (stubRows) Close() error                   { return nil }
func (stubRows) Next([]driver.Value) error      { return io.EOF }
func (stubResult) LastInsertId() (int64, error) { return stubLastInsertID, nil }
func (stubResult) RowsAffected() (int64, error) { return 1, nil }

// stubDB returns a pool backed by the stub driver together with the
// recorder that sees its statements.
func stubDB(t *testing.T) (*sql.DB, *recorder) {
	t.Helper()
	registerDB.Do(func() { sql.Register("eventlog-stub", stubDriver{}) })

	rec := &recorder{}
	openMu.Lock()
	openSeq++
	dsn := "eventlog-" + strconv.Itoa(openSeq)
	recorders[dsn] = rec
	openMu.Unlock()

	db, err := sql.Open("eventlog-stub", dsn)
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	// One connection so a statement always meets the same recorder in
	// the order the test wrote it.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		openMu.Lock()
		delete(recorders, dsn)
		openMu.Unlock()
	})
	return db, rec
}
