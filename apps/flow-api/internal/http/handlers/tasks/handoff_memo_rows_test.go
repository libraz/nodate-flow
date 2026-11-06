package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// The memo write that records a hand-back carries `enabled = TRUE`, so it
// can match no task after the ACL resolved it. MySQL also reports zero
// changed rows when the JSON patch matches the existing memo. These cases
// distinguish the two by checking for the same workspace/task row before
// deciding whether the hand-back event can be recorded.

// TestHandoffWritesTheEventBehindAMemoThatLanded is the positive control.
func TestHandoffWritesTheEventBehindAMemoThatLanded(t *testing.T) {
	t.Parallel()

	stub := &handoffWriteStub{memoRows: 1}
	db := openHandoffStubDB(t, stub)

	err := runHandoffWrites(t, db)
	require.NoError(t, err)

	require.Equal(t, 1, stub.issued("SET agent_memo"), "the hand-back state must be written")
	require.Equal(t, 1, stub.issued("INSERT INTO events"),
		"a hand-back that was recorded must announce itself on the timeline")
}

// TestHandoffAllowsAnUnchangedMemoWhenTheTaskStillExists covers the
// zero-changed-row case where the task remains enabled and in scope.
func TestHandoffAllowsAnUnchangedMemoWhenTheTaskStillExists(t *testing.T) {
	t.Parallel()

	stub := &handoffWriteStub{memoRows: 0, taskExists: true}
	db := openHandoffStubDB(t, stub)

	err := runHandoffWrites(t, db)
	require.NoError(t, err)

	require.Equal(t, 1, stub.issued("SET agent_memo"), "the hand-back state must be attempted")
	require.Equal(t, 1, stub.issued("SELECT t.agent_memo"),
		"a zero changed-row count must be disambiguated by a scoped task lookup")
	require.Equal(t, 1, stub.issued("INSERT INTO events"),
		"an unchanged memo on an existing task must still record the handback event")
}

// TestHandoffRefusesARequestWhoseMemoWriteMatchedNoTask is the absent-row
// case. The request fails with the not-found this handler
// answers with for a task it cannot resolve at all, and the event that
// would have announced the hand-back is never issued.
func TestHandoffRefusesARequestWhoseMemoWriteMatchedNoTask(t *testing.T) {
	t.Parallel()

	stub := &handoffWriteStub{memoRows: 0}
	db := openHandoffStubDB(t, stub)

	err := runHandoffWrites(t, db)
	require.Error(t, err, "a memo write that matched no row must fail the request")

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected ProblemDetails, got %T", err)
	require.Equal(t, apierrors.WsTaskNotFound.Code, problem.Type,
		"a task that stopped being live during the request is the same answer as one that "+
			"could not be resolved at the start of it")

	require.Equal(t, 1, stub.issued("SET agent_memo"), "the request must still attempt the write")
	require.Equal(t, 0, stub.issued("INSERT INTO events"),
		"an event written here announces a hand-back whose state was never recorded")
}

// runHandoffWrites writes the pair of rows both cases write, inside the
// transaction the handler opens, through a recorder with no audit half.
func runHandoffWrites(t *testing.T, db *sql.DB) error {
	t.Helper()
	return dbretry.InTx(context.Background(), db, "test.handoff", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return recordHandoffToUser(ctx, tx, mutationlog.New(db, nil), mutationlog.Actor{UserID: 5, WorkspaceID: 7}, handoffToUserWrites{
			taskID:       42,
			taskPublicID: types.New().String(),
			priorAgentID: 99,
			memoPatch:    []byte(`{"handoff_status":"handed_back"}`),
			payload:      map[string]any{"reason": "done for now"},
		})
	})
}

// --- stub driver ------------------------------------------------------

// handoffWriteStub records the statements that reached it and controls
// both the memo update's changed-row count and the scoped task lookup. A
// stub database/sql driver stands in for MySQL because those are the two
// database outcomes the cases need to distinguish.
type handoffWriteStub struct {
	mu sync.Mutex
	// memoRows is what the agent_memo UPDATE reports as changed.
	memoRows   int64
	taskExists bool
	statements []string
}

func (s *handoffWriteStub) answer(query string) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, query)
	if strings.Contains(query, "SET agent_memo") {
		return handoffWriteResult{rows: s.memoRows}, nil
	}
	// The event INSERT, which needs a last insert id.
	return handoffWriteResult{rows: 1, lastID: int64(len(s.statements))}, nil
}

func (s *handoffWriteStub) issued(needle string) int {
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

type handoffWriteResult struct {
	rows   int64
	lastID int64
}

func (r handoffWriteResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r handoffWriteResult) RowsAffected() (int64, error) { return r.rows, nil }

type handoffWriteRows struct {
	values []driver.Value
	done   bool
}

func (r *handoffWriteRows) Columns() []string { return []string{"agent_memo"} }
func (r *handoffWriteRows) Close() error      { return nil }
func (r *handoffWriteRows) Next(dest []driver.Value) error {
	if r.done || len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}

type (
	handoffWriteDriver struct{}
	handoffWriteConn   struct{ stub *handoffWriteStub }
)

var (
	handoffWriteStubsMu sync.Mutex
	handoffWriteStubs   = map[string]*handoffWriteStub{}
)

func (handoffWriteDriver) Open(dsn string) (driver.Conn, error) {
	handoffWriteStubsMu.Lock()
	defer handoffWriteStubsMu.Unlock()
	stub, ok := handoffWriteStubs[dsn]
	if !ok {
		return nil, fmt.Errorf("tasks: no stub registered for dsn %q", dsn)
	}
	return handoffWriteConn{stub: stub}, nil
}

// Prepare is never reached: the connection answers statements directly,
// so database/sql has no reason to fall back to a prepared statement. It
// fails loudly rather than answering, because a statement that took the
// prepare path would not be recorded and every assertion above would then
// hold vacuously.
func (c handoffWriteConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("tasks: the stub answers statements directly")
}

func (c handoffWriteConn) Close() error { return nil }

func (c handoffWriteConn) Begin() (driver.Tx, error) { return handoffWriteTx{}, nil }

func (c handoffWriteConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	return c.stub.answer(query)
}

func (c handoffWriteConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.stub.mu.Lock()
	c.stub.statements = append(c.stub.statements, query)
	taskExists := c.stub.taskExists
	c.stub.mu.Unlock()
	if !strings.Contains(query, "SELECT t.agent_memo") {
		return nil, fmt.Errorf("tasks: unexpected query %q", query)
	}
	rows := &handoffWriteRows{}
	if taskExists {
		rows.values = []driver.Value{[]byte(`{}`)}
	}
	return rows, nil
}

// CheckNamedValue accepts the argument types these writes bind (uint32
// ids, types.PublicID, sql.NullInt32, json.RawMessage) without
// conversion. The stub reads the SQL, not the arguments.
func (c handoffWriteConn) CheckNamedValue(*driver.NamedValue) error { return nil }

type handoffWriteTx struct{}

func (handoffWriteTx) Commit() error   { return nil }
func (handoffWriteTx) Rollback() error { return nil }

var (
	registerHandoffWriteDriverOnce sync.Once
	handoffWriteDSNSeq             atomic.Int64
)

// openHandoffStubDB builds a pool over the stub.
func openHandoffStubDB(t *testing.T, stub *handoffWriteStub) *sql.DB {
	t.Helper()
	registerHandoffWriteDriverOnce.Do(func() { sql.Register("tasks-handoff-stub", handoffWriteDriver{}) })

	// database/sql keeps a process-wide driver registry, so the per-test
	// state hangs off the DSN and each test takes its own.
	dsn := "handoff-" + strconv.FormatInt(handoffWriteDSNSeq.Add(1), 10)
	handoffWriteStubsMu.Lock()
	handoffWriteStubs[dsn] = stub
	handoffWriteStubsMu.Unlock()
	t.Cleanup(func() {
		handoffWriteStubsMu.Lock()
		delete(handoffWriteStubs, dsn)
		handoffWriteStubsMu.Unlock()
	})

	db, err := sql.Open("tasks-handoff-stub", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
