package helpers

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/dbretry"
)

// Execer is the subset of *sql.DB that ExecRetry needs. Keeping it an
// interface lets the helper accept either a *sql.DB or any wrapper that
// exposes ExecContext without forcing a concrete type on callers.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ExecRetry runs a single autocommit statement, retrying on transient
// MySQL errors (InnoDB deadlock 1213, lock-wait-timeout 1205) via
// dbretry.Do. Parallel e2e seeds insert rows into the events table and
// contend on FK record locks against concurrent seeds and cleanup, so a
// raw db.Exec occasionally fails with a deadlock under heavy load. The
// production append paths already retry through dbretry; this gives the
// test seeds the same protection so they do not fail spuriously.
//
// The query must be a single autocommit statement: retrying is only
// safe because a deadlocked autocommit statement is fully rolled back
// before the retry re-issues it, so no partial state survives. For a
// multi-statement seed wrap the whole transaction with dbretry.InTx
// instead of calling ExecRetry per statement.
//
// label identifies the call site in the dbretry slog warn/error lines.
func ExecRetry(ctx context.Context, db Execer, label, query string, args ...any) (sql.Result, error) {
	var res sql.Result
	err := dbretry.Do(ctx, label, func(ctx context.Context) error {
		r, execErr := db.ExecContext(ctx, query, args...)
		if execErr != nil {
			return execErr
		}
		res = r
		return nil
	})
	return res, err
}
