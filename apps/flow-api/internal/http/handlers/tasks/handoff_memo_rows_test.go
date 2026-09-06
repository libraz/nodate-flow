package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// The memo write that records a hand-back carries `enabled = TRUE`, so it
// can succeed and match no row: the task ACL resolved the task before the
// request opened its transaction, and it stopped being live in between.
// The event written after it is the visible half of the hand-back, so a
// request that recorded the event over an unwritten memo would look, to
// every reader afterwards, exactly like one that succeeded — an
// announcement on the timeline above a handoff_status that never changed.
//
// The two cases below run the same writes with the same arguments,
// differing only in what the driver reports as the memo write's
// affected-row count. The pairing is the evidence: the absent event in
// the zero-row case means something only because the one-row case shows
// these inputs do produce it.

// TestHandoffWritesTheEventBehindAMemoThatLanded is the positive control.
func TestHandoffWritesTheEventBehindAMemoThatLanded(t *testing.T) {
	t.Parallel()

	stub := &handoffWriteStub{memoRows: 1}
	db := openHandoffStubDB(t, stub)

	err := recordHandoffToUser(context.Background(), generated.New(db), handoffWrites())
	require.NoError(t, err)

	require.Equal(t, 1, stub.issued("SET agent_memo"), "the hand-back state must be written")
	require.Equal(t, 1, stub.issued("INSERT INTO events"),
		"a hand-back that was recorded must announce itself on the timeline")
}

// TestHandoffRefusesARequestWhoseMemoWriteMatchedNoTask is the case the
// count answers. The request fails with the not-found this handler
// answers with for a task it cannot resolve at all, and the event that
// would have announced the hand-back is never issued.
func TestHandoffRefusesARequestWhoseMemoWriteMatchedNoTask(t *testing.T) {
	t.Parallel()

	stub := &handoffWriteStub{memoRows: 0}
	db := openHandoffStubDB(t, stub)

	err := recordHandoffToUser(context.Background(), generated.New(db), handoffWrites())
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

// handoffWrites is the pair of rows both cases write.
func handoffWrites() handoffToUserWrites {
	return handoffToUserWrites{
		workspaceID:   7,
		taskID:        42,
		priorAgentID:  99,
		eventPublicID: types.New(),
		memoPatch:     []byte(`{"handoff_status":"handed_back"}`),
		payload:       []byte(`{"reason":"done for now"}`),
		occurredAt:    time.Unix(1_700_000_000, 0).UTC(),
	}
}

// --- stub driver ------------------------------------------------------

// handoffWriteStub records the statements that reached it and decides
// what the memo write reports as affected. A stub database/sql driver
// stands in for MySQL because the count is the one thing these cases have
// to control, and it is exactly what a driver decides.
type handoffWriteStub struct {
	mu sync.Mutex
	// memoRows is what the agent_memo UPDATE reports as affected. Zero is
	// the task having been disabled or removed since the ACL resolved it.
	memoRows   int64
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
