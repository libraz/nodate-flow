package taskcreate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

// The body a task is created with is the first version of its
// description, and it is written here because every creating path reaches
// this function. The event naming that version has to be written here for
// the same reason: a timeline entry emitted by some transports and not by
// others describes a version history whose entries mean different things
// depending on which client wrote them.
//
// The two tests below create a task through the same function with the
// same attribution, differing only in whether the task has a description
// at all. That pairing is the evidence: an empty body writes no version
// row, so the absence of an event there only means something because the
// case with a body shows the event is produced from these inputs.
//
// A stub database/sql driver stands in for MySQL, which is enough because
// the question is which statements the function issues and what it binds
// to them.

// createStub records the statements one stub-backed pool received.
type createStub struct {
	mu         sync.Mutex
	statements []recordedStatement
}

// recordedStatement is one statement and the values bound to it.
type recordedStatement struct {
	sql  string
	args []driver.Value
}

func (s *createStub) record(query string, args []driver.NamedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vals := make([]driver.Value, 0, len(args))
	for _, a := range args {
		vals = append(vals, a.Value)
	}
	s.statements = append(s.statements, recordedStatement{sql: query, args: vals})
}

// issued counts the statements containing needle.
func (s *createStub) issued(needle string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, st := range s.statements {
		if strings.Contains(st.sql, needle) {
			n++
		}
	}
	return n
}

// only returns the single statement containing needle.
func (s *createStub) only(t *testing.T, needle string) recordedStatement {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	var found []recordedStatement
	for _, st := range s.statements {
		if strings.Contains(st.sql, needle) {
			found = append(found, st)
		}
	}
	require.Len(t, found, 1, "want exactly one statement containing %q", needle)
	return found[0]
}

// count returns how many statements have been recorded, which is what the
// stub answers an INSERT's last insert id with.
func (s *createStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.statements)
}

// bindings pairs an INSERT's column list with the values bound to it,
// read out of the statement itself so a column order that moves is a
// failure here rather than a silently wrong assertion.
func bindings(t *testing.T, st recordedStatement) map[string]driver.Value {
	t.Helper()

	open := strings.Index(st.sql, "(")
	shut := strings.Index(st.sql, ")")
	require.Greater(t, shut, open, "the statement names no column list: %s", st.sql)
	cols := strings.Split(st.sql[open+1:shut], ",")
	require.Len(t, st.args, len(cols), "the statement binds a different number of values than it names columns")

	out := make(map[string]driver.Value, len(cols))
	for i, c := range cols {
		out[strings.TrimSpace(c)] = st.args[i]
	}
	return out
}

type createResult struct{ rows, lastID int64 }

func (r createResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r createResult) RowsAffected() (int64, error) { return r.rows, nil }

type (
	createDriver struct{}
	createConn   struct{ stub *createStub }
	createTx     struct{ stub *createStub }
)

var (
	createStubsMu sync.Mutex
	createStubs   = map[string]*createStub{}
)

func (createDriver) Open(dsn string) (driver.Conn, error) {
	createStubsMu.Lock()
	defer createStubsMu.Unlock()
	stub, ok := createStubs[dsn]
	if !ok {
		return nil, fmt.Errorf("taskcreate: no stub registered for dsn %q", dsn)
	}
	return createConn{stub: stub}, nil
}

// Prepare is never reached: the connection answers statements directly,
// so database/sql has no reason to fall back to a prepared statement. It
// fails loudly rather than answering, because a statement that took the
// prepare path would not be recorded and every assertion below would then
// hold vacuously.
func (c createConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("taskcreate: the stub answers statements directly")
}

func (c createConn) Close() error { return nil }

func (c createConn) Begin() (driver.Tx, error) { return createTx(c), nil }

func (c createConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.stub.record(query, args)
	return createResult{rows: 1, lastID: int64(c.stub.count())}, nil
}

// QueryContext answers the three single-value reads a create makes: the
// project row it locks, the task number it allocates, and the version
// number the snapshot claims. An unrecognised read fails rather than
// returning an empty page, which would leave a create silently taking a
// path no test meant to exercise.
func (c createConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.stub.record(query, args)
	switch {
	case strings.Contains(query, "FROM projects"):
		return &singleValueRows{col: "id", value: int64(createProjectID)}, nil
	case strings.Contains(query, "MAX(task_number)"):
		return &singleValueRows{col: "next_number", value: int64(1)}, nil
	case strings.Contains(query, "MAX(version_number)"):
		return &singleValueRows{col: "next_version", value: int64(createVersionNumber)}, nil
	}
	return nil, fmt.Errorf("taskcreate: the stub has no answer for %q", query)
}

func (t createTx) Commit() error   { return nil }
func (t createTx) Rollback() error { return nil }

// singleValueRows is a one-row, one-column page.
type singleValueRows struct {
	col   string
	value driver.Value
	done  bool
}

func (r *singleValueRows) Columns() []string { return []string{r.col} }
func (r *singleValueRows) Close() error      { return nil }

func (r *singleValueRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

const (
	createWorkspaceID   = 3
	createProjectID     = 11
	createAuthorID      = 42
	createVersionNumber = 1
)

var (
	registerCreateDriverOnce sync.Once
	createDSNSeq             atomic.Int64
)

// newCreateDB opens a pool backed by its own stub.
func newCreateDB(t *testing.T) (*sql.DB, *createStub) {
	t.Helper()
	registerCreateDriverOnce.Do(func() { sql.Register("taskcreate-stub", createDriver{}) })

	// database/sql keeps a process-wide driver registry, so the per-test
	// state hangs off the DSN and each test takes its own.
	dsn := "create-" + strconv.FormatInt(createDSNSeq.Add(1), 10)
	stub := &createStub{}
	createStubsMu.Lock()
	createStubs[dsn] = stub
	createStubsMu.Unlock()
	t.Cleanup(func() {
		createStubsMu.Lock()
		delete(createStubs, dsn)
		createStubsMu.Unlock()
	})

	db, err := sql.Open("taskcreate-stub", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, stub
}

// createTask runs one create with the given description.
func createTask(t *testing.T, db *sql.DB, description string) Result {
	t.Helper()

	title, err := taskrules.NewTitle("A task")
	require.NoError(t, err)

	var created Result
	require.NoError(t, dbretry.InTx(context.Background(), db, "test.create", nil,
		func(ctx context.Context, tx *dbretry.Tx) error {
			var cerr error
			created, cerr = New(ctx, tx, AuthoredBy(createAuthorID), Args{
				WorkspaceID: createWorkspaceID,
				ProjectID:   createProjectID,
				Title:       title,
				Description: sql.NullString{String: description, Valid: description != ""},
			})
			return cerr
		}))
	return created
}

// TestCreateAnnouncesTheDescriptionVersionItWrote is the positive
// control. It is what makes the absence asserted below evidence rather
// than a test that could never have seen an event in the first place.
func TestCreateAnnouncesTheDescriptionVersionItWrote(t *testing.T) {
	t.Parallel()

	db, stub := newCreateDB(t)
	created := createTask(t, db, "The first body this task carried")

	require.Equal(t, 1, stub.issued("INSERT INTO task_description_versions"),
		"a task created with a body starts its history with that body")
	require.Equal(t, 1, stub.issued("INSERT INTO events"),
		"a version nothing announces is a history entry no timeline reader can see")

	bound := bindings(t, stub.only(t, "INSERT INTO events"))
	require.Equal(t, string(eventbus.DescriptionVersionCreated), bound["type"])
	require.EqualValues(t, createWorkspaceID, bound["workspace_id"])
	require.EqualValues(t, createAuthorID, bound["actor_user_id"],
		"the version is attributed to whoever the task was authored by")

	raw, ok := bound["payload_json"].([]byte)
	require.True(t, ok, "the payload must reach the driver as bytes")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, created.PublicID.String(), payload["taskId"],
		"the payload names the task by its public id")

	// The version row and the event naming it have to name the same row,
	// or the entry points at a version nobody can open.
	versionRow := bindings(t, stub.only(t, "INSERT INTO task_description_versions"))
	pub, ok := versionRow["public_id"].([]byte)
	require.True(t, ok, "the version's public id must reach the driver as bytes")
	require.Equal(t, uuidString(pub), payload["versionId"])
	require.EqualValues(t, createVersionNumber, payload["versionNumber"])
}

// TestCreateWithoutADescriptionAnnouncesNothing is the case the empty
// body answers.
//
// Same function, same attribution, same statements — the task simply has
// no description, so no version row is written. Nothing was created, so
// nothing may say it was.
func TestCreateWithoutADescriptionAnnouncesNothing(t *testing.T) {
	t.Parallel()

	db, stub := newCreateDB(t)
	createTask(t, db, "")

	require.Equal(t, 0, stub.issued("INSERT INTO task_description_versions"),
		"an absent description is not a version of one")
	require.Equal(t, 0, stub.issued("INSERT INTO events"),
		"a create that wrote no version announced one; the entry points at a row that does not exist")
}

// uuidString renders 16 raw bytes the way a public id is written in a
// payload, so an id bound to a column and the same id named in JSON can
// be compared.
func uuidString(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}
