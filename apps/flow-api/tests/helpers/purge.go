package helpers

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// MySQL error numbers for "this row is still referenced by a foreign
// key". A delete that hits one of these is not a failure, only a
// statement issued too early: the referencing table has not been
// emptied yet.
const (
	errRowIsReferenced  = 1217
	errRowIsReferenced2 = 1451
)

// scopedTablesCache memoises the workspace-scoped table list per
// database handle. The schema cannot change while a test binary runs,
// and the lookup is a full information_schema scan, so reading it once
// per handle keeps per-test cleanup cheap.
var (
	scopedTablesMu    sync.Mutex
	scopedTablesCache = map[*sql.DB][]string{}
)

// WorkspaceScopedTables returns every table in the connected schema
// that carries a workspace_id column, sorted by name.
//
// The list is read from information_schema rather than written out by
// hand. A hand-maintained list silently goes stale the moment a table
// is added — the cleanup keeps passing, the rows keep accumulating, and
// the damage shows up much later as a test that only fails when the
// whole suite runs.
func WorkspaceScopedTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	scopedTablesMu.Lock()
	defer scopedTablesMu.Unlock()
	if cached, ok := scopedTablesCache[db]; ok {
		return cached
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// information_schema.columns also reports the workspace_id a view
	// selects, and a view is neither deletable nor a place rows can
	// survive, so the join narrows the answer to base tables.
	rows, err := db.QueryContext(ctx, `
		SELECT c.table_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables  t
		    ON t.table_schema = c.table_schema
		   AND t.table_name   = c.table_name
		 WHERE c.table_schema = DATABASE()
		   AND c.column_name  = 'workspace_id'
		   AND t.table_type   = 'BASE TABLE'`)
	require.NoError(t, err, "list workspace-scoped tables")
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, tables, "no workspace-scoped tables found; is the schema applied?")
	sort.Strings(tables)

	scopedTablesCache[db] = tables
	return tables
}

// WorkspaceInternalID resolves a workspace public id to its internal
// auto-increment id. The second return value is false when no such
// workspace exists.
func WorkspaceInternalID(t *testing.T, db *sql.DB, workspacePublicID string) (uint32, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wsID uint32
	err := db.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID).Scan(&wsID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	require.NoError(t, err, "resolve workspace internal id")
	return wsID, true
}

// WorkspaceResidue counts the rows still keyed to wsID in every
// workspace-scoped table, returning only the tables that have any. An
// empty result means the workspace left nothing behind.
func WorkspaceResidue(t *testing.T, db *sql.DB, wsID uint32) map[string]int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	residue := map[string]int64{}
	for _, table := range WorkspaceScopedTables(t, db) {
		var n int64
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+quoteIdent(t, table)+" WHERE workspace_id = ?",
			wsID).Scan(&n)
		require.NoErrorf(t, err, "count rows in %s", table)
		if n > 0 {
			residue[table] = n
		}
	}
	return residue
}

// PurgeWorkspace removes every row that belongs to the supplied
// workspace public id, using direct SQL.
//
// IMPORTANT: this is the ONLY direct-SQL exception in the test suite.
// It exists because the workspace API does not yet expose a delete
// operation. Once the workspace delete API lands, callers should switch
// to that route and PurgeWorkspace should be deleted.
//
// Foreign key enforcement stays on for the whole purge, deliberately.
// Turning it off costs more than it saves: the session variable is not
// transactional, so a rollback — or a failed assertion unwinding the
// stack — hands the connection back to the pool with enforcement still
// disabled, and every later test that draws that connection runs
// without it. Worse, MySQL does not run ON DELETE CASCADE while checks
// are off, so disabling them to avoid thinking about delete order also
// suppresses the cascades that were supposed to reach the tables the
// delete list does not name.
//
// The work then splits in two. Deleting the workspace row cascades to
// every table that names it, which is most of them, and that alone
// would clear a workspace whose graph is all CASCADE. The sweep exists
// for the rows that cascade cannot reach in any order MySQL happens to
// pick: attachments and calendar_event_attachments reference
// storage_objects with ON DELETE RESTRICT while all three cascade from
// the workspace, so the cascade can arrive at storage_objects with a
// referencing row still in place and refuse the whole delete. Emptying
// the tables first takes that ordering out of InnoDB's hands.
//
// The sweep runs in repeated passes: a delete rejected because another
// table still references its rows is retried on the next pass, once
// that table has been emptied. The loop stops when a pass makes no
// progress, which means a reference the sweep cannot resolve — a real
// defect, reported rather than skipped.
func PurgeWorkspace(t *testing.T, db *sql.DB, workspacePublicID string) {
	t.Helper()
	if workspacePublicID == "" {
		return
	}
	wsID, ok := WorkspaceInternalID(t, db, workspacePublicID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// calendar_events rows projected from a task are protected by the
	// projection guard, which only stands aside for the projection
	// engine. sql/core/PROTOCOL.md names the opt-in variable and sets
	// the terms for taking that role: it is session state, so it has to
	// be set on the connection doing the write and cleared before that
	// connection goes back to the pool — a connection handed back with
	// the guard down gives an unrelated test a database with one fewer
	// invariant, and neither a rollback nor the driver's session reset
	// puts it back. Pinning one connection for the whole purge, and
	// clearing on the way out of every path, is what makes both halves
	// hold.
	conn, err := db.Conn(ctx)
	require.NoError(t, err, "pin a connection for the purge")
	defer func() {
		// A fresh context: the one above may already have expired, and
		// the guard has to come down either way.
		resetCtx, resetCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer resetCancel()
		_, resetErr := conn.ExecContext(resetCtx, "SET @nf_item_projection_engine = NULL")
		closeErr := conn.Close()
		require.NoError(t, resetErr, "clear the projection guard opt-in")
		require.NoError(t, closeErr, "release the purge connection")
	}()
	_, err = conn.ExecContext(ctx, "SET @nf_item_projection_engine = 1")
	require.NoError(t, err, "take the projection engine role for the purge")

	remaining := WorkspaceScopedTables(t, db)
	for len(remaining) > 0 {
		var blocked []string
		for _, table := range remaining {
			// Most tables hold nothing for any one workspace, and a
			// DELETE still takes a next-key lock over the workspace_id
			// range it scans. Workspace ids are adjacent integers, so
			// that lock covers the gap where a parallel test is
			// inserting rows for the workspace next door, and the two
			// deadlock over a table neither of them has data in. The
			// snapshot read below takes no locks.
			if !hasWorkspaceRows(ctx, t, conn, table, wsID) {
				continue
			}
			_, err := ExecRetry(ctx, conn, "helpers.PurgeWorkspace",
				"DELETE FROM "+quoteIdent(t, table)+" WHERE workspace_id = ?", wsID)
			switch {
			case err == nil:
			case isRowReferenced(err):
				blocked = append(blocked, table)
			default:
				require.NoErrorf(t, err, "purge %s", table)
			}
		}
		if len(blocked) == len(remaining) {
			require.FailNowf(t, "PurgeWorkspace made no progress",
				"tables still referenced after a full pass: %v", blocked)
		}
		remaining = blocked
	}

	// The workspace row goes last, and its cascade is what clears any
	// table the sweep did not have to touch. The sweep ran first so that
	// this statement never meets a RESTRICT edge halfway down the chain.
	_, err = ExecRetry(ctx, conn, "helpers.PurgeWorkspace",
		`DELETE FROM workspaces WHERE id = ?`, wsID)
	require.NoError(t, err, "delete workspace row")
}

// hasWorkspaceRows reports whether the table holds at least one row for
// the workspace. It is a plain snapshot read, so it takes no locks.
func hasWorkspaceRows(ctx context.Context, t *testing.T, conn *sql.Conn, table string, wsID uint32) bool {
	t.Helper()
	var one int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM "+quoteIdent(t, table)+" WHERE workspace_id = ? LIMIT 1",
		wsID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	require.NoErrorf(t, err, "probe %s", table)
	return true
}

// isRowReferenced reports whether err is MySQL refusing a delete
// because another table still references the rows.
func isRowReferenced(err error) bool {
	var myErr *mysql.MySQLError
	if !errors.As(err, &myErr) {
		return false
	}
	return myErr.Number == errRowIsReferenced || myErr.Number == errRowIsReferenced2
}

// quoteIdent back-quotes a table name for interpolation into a
// statement. The names come from information_schema, but they are still
// checked against the identifier character set the schema uses so a
// surprising name fails the test instead of reaching the server.
func quoteIdent(t *testing.T, name string) string {
	t.Helper()
	require.NotEmpty(t, name, "empty table name")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			require.Failf(t, "unsupported table name", "table %q contains %q", name, r)
		}
	}
	return "`" + name + "`"
}
