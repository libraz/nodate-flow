// Package dbretry provides the shared retry policy for transactions
// that fail with a transient MySQL error.
//
// Two error classes are eligible for retry:
//
//   - ER_LOCK_DEADLOCK (1213): InnoDB detected a deadlock and rolled
//     back this transaction. The standard fix is to re-issue the work;
//     locks are re-acquired in a fresh order on the retry.
//   - ER_LOCK_WAIT_TIMEOUT (1205): the statement waited longer than
//     innodb_lock_wait_timeout for a row lock. Less common in practice
//     than 1213 but still transient — retrying is appropriate when the
//     contending transaction has since released its lock.
//
// InnoDB invalidates the whole transaction on a deadlock, so retrying a
// single statement inside the dead transaction is meaningless: the unit
// of retry is begin → work → commit, which is what [InTx] wraps.
//
// Both services share this policy so a deadlock means the same thing on
// either side of the API: a request that succeeds on the second attempt
// rather than a 500 the user reads as "creating a workspace is broken".
// flow-api re-exports these names under its own import path; the
// implementation lives here so both event appenders agree on it.
//
// The package also owns [CommitBoundary], the handle type that says when
// a write becomes durable. Work that must follow a commit registers on
// the handle rather than on the context, so it cannot be separated from
// the transaction it is waiting for.
package dbretry

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// commitHooks collects callbacks to run only after the enclosing
// transaction commits successfully. A [Tx] owns one, so the collector
// travels with the handle the statements are issued against rather than
// alongside it: a handle and a context can be separated at a call site,
// and when they are, work registered here would wait for a commit that
// nothing reports.
//
// Deferring matters for two reasons: fan-out goroutines run on a
// separate connection and would otherwise contend on FK locks the
// still-open transaction holds against its own freshly-inserted rows (a
// self-inflicted deadlock source), and a hook that fires before a commit
// that later rolls back announces an event that never happened.
type commitHooks struct {
	mu  sync.Mutex
	fns []func()
}

// add appends fn to the collector.
func (c *commitHooks) add(fn func()) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.fns = append(c.fns, fn)
	c.mu.Unlock()
}

// run fires and clears every registered callback.
func (c *commitHooks) run() {
	c.mu.Lock()
	fns := c.fns
	c.fns = nil
	c.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// CommitBoundary is a database handle that knows when the statements
// issued through it become durable, and can therefore hold work back
// until they are.
//
// The interface is sealed: [Tx] and [AutoCommitDB] are its only
// implementations, and no type outside this package can add one. A bare
// *sql.Tx and a bare *sql.DB deliberately do not satisfy it.
//
// That is the whole point. A transaction opened by hand has no commit
// boundary anyone can observe, so side effects that must follow the
// commit — realtime fan-out, notification and webhook delivery — have
// nothing to wait for and are silently lost. Code that registers such
// work takes a CommitBoundary, which makes the pairing a compile error
// instead of a delivery that quietly evaporates.
type CommitBoundary interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row

	// AfterCommit registers fn to run once the writes made through this
	// handle are durable. Inside a transaction fn waits for the commit
	// and is dropped if the transaction rolls back; on the auto-commit
	// path the write is already durable and fn runs immediately.
	AfterCommit(fn func())

	// RunStatement runs fn under the retry policy this handle's commit
	// boundary allows. label names the operation in the retry logs.
	RunStatement(ctx context.Context, label string, fn func(ctx context.Context) error) error

	// Fail reports err as the loss of a write the rest of this unit must
	// not outlive. Inside a transaction the commit is refused and
	// everything issued through the handle rolls back; on the auto-commit
	// path the surrounding statements are already durable and there is
	// nothing left to withhold, so the call is a no-op.
	//
	// It exists for writes whose absence silently corrupts something
	// derived from them — an event row a projection reads state from,
	// say. Reporting the failure to the caller is not enough there,
	// because the caller can log it and carry on and the corruption then
	// looks like a healthy request. Fail takes that option away from the
	// call site: whatever it does with the returned error, the
	// transaction it was working in does not commit.
	Fail(err error)

	// isCommitBoundary seals the interface to this package.
	isCommitBoundary()
}

// Tx is the transaction [InTx] hands to its closure: a *sql.Tx together
// with the collector for work that must not run until it commits.
//
// Owning the collector is what makes the type meaningful. While the
// collector lived on the context, the transaction and its boundary were
// two values that a call site could pass separately — and appending an
// event with the transaction but a different context produced a write
// whose fan-out no commit would ever release.
type Tx struct {
	tx    *sql.Tx
	hooks commitHooks

	// failMu guards failure. A closure may issue its statements from more
	// than one goroutine, and the whole point of the field is that it is
	// read on a path the closure does not control.
	failMu sync.Mutex
	// failure is the first error reported through [Tx.Fail]. Holding the
	// first rather than the last keeps the reason the transaction was
	// abandoned, instead of whatever failed afterwards as a consequence.
	failure error
}

func (t *Tx) isCommitBoundary() {}

// ExecContext runs the statement inside the transaction.
func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

// PrepareContext prepares the statement on the transaction's connection.
func (t *Tx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return t.tx.PrepareContext(ctx, query)
}

// QueryContext runs the query inside the transaction.
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext runs the single-row query inside the transaction.
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// AfterCommit registers fn to run after this transaction commits. Hooks
// registered by an attempt that rolls back are discarded with the
// attempt: [InTx] builds a fresh Tx each time round.
func (t *Tx) AfterCommit(fn func()) { t.hooks.add(fn) }

// RunStatement runs fn once. Retrying a single statement inside a
// transaction is meaningless: InnoDB invalidates the whole transaction
// on a deadlock, so the statement would be re-issued against a
// transaction the server has already rolled back. The unit of retry is
// the transaction, which [InTx] already wraps.
func (t *Tx) RunStatement(ctx context.Context, _ string, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// Fail marks the transaction as one that must not commit. [InTx] then
// rolls it back and returns err, whatever the closure itself returned.
//
// This is what makes a lost write unswallowable rather than merely
// reported. A call site can log the error it got back and continue; it
// cannot make the surrounding transaction durable afterwards, so the
// half-written state a swallowed error would otherwise produce has
// nowhere to land. Passing nil is a no-op so callers can forward an error
// unconditionally.
func (t *Tx) Fail(err error) {
	if err == nil {
		return
	}
	t.failMu.Lock()
	if t.failure == nil {
		t.failure = err
	}
	t.failMu.Unlock()
}

// failed returns the error reported through [Tx.Fail], or nil.
func (t *Tx) failed() error {
	t.failMu.Lock()
	defer t.failMu.Unlock()
	return t.failure
}

// RawTx returns the underlying *sql.Tx.
//
// It is an escape hatch, needed because some APIs cannot be typed in
// terms of [CommitBoundary]: sqlc's generated (*Queries).WithTx takes a
// *sql.Tx, and so do helpers that only run statements in the
// transaction.
//
// Do not hand the result to something that appends to the event log.
// Stripping the wrapper strips the commit boundary with it, and work
// deferred to a boundary that no longer exists is exactly what
// [CommitBoundary] refuses to let compile.
func (t *Tx) RawTx() *sql.Tx { return t.tx }

// AutoCommitDB is a *sql.DB addressed as a commit boundary: each
// statement issued through it commits on its own, so there is no
// boundary to defer to and post-commit work runs immediately.
//
// Appending through it is legitimate. The type exists so that choosing
// it is something the caller writes down rather than something that
// happens because a *sql.DB was the handle nearest to hand.
type AutoCommitDB struct{ db *sql.DB }

// AutoCommit declares that statements issued through db commit
// individually, with no enclosing transaction to wait for.
func AutoCommit(db *sql.DB) AutoCommitDB { return AutoCommitDB{db: db} }

func (a AutoCommitDB) isCommitBoundary() {}

// ExecContext runs the statement on its own transaction.
func (a AutoCommitDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.db.ExecContext(ctx, query, args...)
}

// PrepareContext prepares the statement on the pool.
func (a AutoCommitDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return a.db.PrepareContext(ctx, query)
}

// QueryContext runs the query on the pool.
func (a AutoCommitDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return a.db.QueryContext(ctx, query, args...)
}

// QueryRowContext runs the single-row query on the pool.
func (a AutoCommitDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return a.db.QueryRowContext(ctx, query, args...)
}

// AfterCommit runs fn immediately: the statement it follows is already
// durable, so there is nothing to wait for.
func (a AutoCommitDB) AfterCommit(fn func()) {
	if fn == nil {
		return
	}
	fn()
}

// Fail is a no-op: each statement issued through this handle committed
// on its own, so by the time a later one fails there is nothing left to
// withhold. Callers on this path get the error back and are on their own
// with it — the static guard on the append entry points is what stands
// in for the missing boundary here.
func (a AutoCommitDB) Fail(error) {}

// RunStatement wraps fn in the deadlock retry. Parallel writers contend
// on FK record locks for shared parents, and InnoDB resolves the
// contention by rolling one side back; re-issuing the single statement
// clears it because the statement is the whole transaction here.
func (a AutoCommitDB) RunStatement(ctx context.Context, label string, fn func(ctx context.Context) error) error {
	return Do(ctx, label, fn)
}

// MySQL transient error numbers eligible for retry. The numeric values
// match server source (sql/share/errmsg-utf8.txt).
const (
	errLockDeadlock    uint16 = 1213
	errLockWaitTimeout uint16 = 1205
)

// MaxAttempts is the upper bound on retry rounds (initial attempt +
// retries). Three is enough to ride out the typical two-party deadlock
// without amplifying load when the schema is genuinely contended.
const MaxAttempts = 3

// baseBackoff is the unjittered base delay applied between retry
// rounds; the actual sleep is base*attempt with up to ±50% jitter.
const baseBackoff = 5 * time.Millisecond

// IsTransient reports whether err is a MySQL deadlock or
// lock-wait-timeout that should be retried by the caller.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var me *mysql.MySQLError
	if !errors.As(err, &me) {
		return false
	}
	return me.Number == errLockDeadlock || me.Number == errLockWaitTimeout
}

// Do runs fn, retrying up to [MaxAttempts] when fn returns a transient
// MySQL error. The supplied label is included in the slog warn/error
// lines so dashboards can attribute retries to a call site without
// inspecting the stack.
//
// The supplied context bounds the total wall time of the retry
// schedule: if ctx is cancelled between attempts the most recent error
// is returned without further retries.
func Do(ctx context.Context, label string, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			if attempt > 1 {
				slog.InfoContext(ctx, "dbretry: succeeded after retry",
					slog.String("op", label),
					logutil.LogNumber("attempt", attempt))
			}
			return nil
		}
		if !IsTransient(err) {
			return err
		}
		lastErr = err
		if attempt == MaxAttempts {
			break
		}
		slog.WarnContext(ctx, "dbretry: transient mysql error, retrying",
			slog.String("op", label),
			logutil.LogNumber("attempt", attempt),
			slog.String("err", err.Error()))
		// Jittered linear back-off: base * attempt * (0.5..1.5).
		// math/rand/v2 is concurrency-safe and seeded automatically; no
		// need for crypto-grade randomness here.
		j := 0.5 + rand.Float64() //nolint:gosec // non-cryptographic jitter
		sleep := time.Duration(float64(baseBackoff) * float64(attempt) * j)
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return lastErr
		}
	}
	slog.ErrorContext(ctx, "dbretry: exhausted retries",
		slog.String("op", label),
		logutil.LogNumber("attempts", MaxAttempts),
		slog.String("err", lastErr.Error()))
	return lastErr
}

// TxBeginner is the subset of *sql.DB needed by [InTx]. The interface
// keeps test doubles small without forcing callers to spin up a real
// MySQL connection.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// InTx runs fn inside a fresh transaction, retrying the whole
// transaction (begin → fn → commit) on transient MySQL errors.
//
// fn must be re-entrant: it will be called again on retry. In practice
// that means avoiding side effects between begin and commit that are
// not transaction scoped — publishing to a queue, sending mail, writing
// to object storage. Non-transactional fan-out that must observe the
// committed row should be registered with [Tx.AfterCommit]; InTx runs
// those callbacks only after the commit succeeds and drops them when an
// attempt rolls back, so a retried or aborted transaction never leaks a
// spurious event.
//
// A transaction that had a write reported through [Tx.Fail] does not
// commit, whatever fn returned. That is deliberate: it removes the
// option of noticing such a failure, logging it, and letting the rest of
// the unit land anyway.
//
// opts is forwarded to BeginTx untouched. Pass nil for the default
// isolation level.
func InTx(ctx context.Context, db TxBeginner, label string, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) error {
	return Do(ctx, label, func(ctx context.Context) error {
		sqlTx, err := db.BeginTx(ctx, opts)
		if err != nil {
			return err
		}
		// Fresh wrapper per attempt: hooks registered by an attempt that
		// later rolls back go away with it.
		tx := &Tx{tx: sqlTx}
		// Rollback on every path that does not commit. Calling Rollback
		// after a successful Commit is a no-op the driver swallows
		// (sql.ErrTxDone), so the deferred cleanup is unconditional.
		committed := false
		defer func() {
			if !committed {
				_ = sqlTx.Rollback()
			}
		}()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		// A write reported through [Tx.Fail] is one whose loss corrupts
		// something derived from it, so the transaction it belonged to must
		// not become durable without it — even when the closure returned
		// nil. Checking here rather than trusting the closure is the point:
		// it is what stops a call site from logging such a failure and
		// carrying on to commit the rest.
		if err := tx.failed(); err != nil {
			return err
		}
		if err := sqlTx.Commit(); err != nil {
			return err
		}
		committed = true
		// Commit succeeded and locks are released; fire the deferred
		// fan-out now so it observes the committed rows without
		// contending on this transaction's locks.
		tx.hooks.run()
		return nil
	})
}
