// Package sessionguard arms and disarms the MySQL session variables that
// let an engine write through a guard trigger.
//
// The variables are scoped to the connection, so a writer that leaves one
// set hands an unguarded connection to an unrelated request. Arm is the
// only place that sets or clears them: the disarm runs on a context that
// outlives cancellation, and a connection whose disarm fails is discarded
// or, inside a transaction, forbidden to commit.
package sessionguard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"
)

// Var names a session variable read by a guard trigger.
type Var string

const (
	// DerivedStateEngine admits writes to tasks.derived_state
	// (trg_tasks_derived_state_guard).
	DerivedStateEngine Var = "nf_derived_state_engine"
	// ItemProjectionEngine admits writes to task-projected calendar_events
	// rows (trg_calendar_events_projection_guard_*).
	ItemProjectionEngine Var = "nf_item_projection_engine"
)

// disarmTimeout bounds the disarm so a wedged connection cannot hold the
// caller's return indefinitely.
const disarmTimeout = 5 * time.Second

// Execer is the statement surface Arm needs: a *sql.Conn, *sql.Tx or
// *dbretry.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// failer is implemented by a transaction that can be forbidden to commit.
type failer interface{ Fail(err error) }

// rawer is implemented by a pinned connection that can be discarded.
type rawer interface {
	Raw(f func(driverConn any) error) error
}

// Arm sets v on the connection behind ex and returns the function that
// clears it. Call the returned function on every exit path, before the
// connection is released or the transaction ends.
//
// If the clear fails, a pinned *sql.Conn is marked bad so the pool drops
// it, and a transaction is failed so it cannot commit.
func Arm(ctx context.Context, ex Execer, v Var) (func(), error) {
	if _, err := ex.ExecContext(ctx, "SET @"+string(v)+" = 1"); err != nil {
		return nil, err
	}
	return func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), disarmTimeout)
		defer cancel()
		_, err := ex.ExecContext(dctx, "SET @"+string(v)+" = NULL")
		if err == nil {
			return
		}
		switch c := ex.(type) {
		case rawer:
			_ = c.Raw(func(any) error { return driver.ErrBadConn })
		case failer:
			c.Fail(err)
		}
	}, nil
}
