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
// flow-api layers commit hooks on top in its own db/dbretry package;
// the classification and back-off schedule live here.
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
)

// commitHookKey is the context key under which [InTx] stores the
// per-attempt commit-hook collector.
type commitHookKey struct{}

// commitHooks collects callbacks to run only after the enclosing
// transaction commits successfully. It is attached to the context
// [InTx] passes to fn so transaction-aware code — the two event
// appenders — can defer non-transactional side effects (realtime
// fan-out, notification and webhook goroutines) until the row is
// durably committed.
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

// WithCommitHooks returns a child context carrying a fresh commit-hook
// collector. [InTx] calls this once per transaction attempt so hooks
// registered by a rolled-back attempt are discarded and never fire.
func WithCommitHooks(ctx context.Context) context.Context {
	return context.WithValue(ctx, commitHookKey{}, &commitHooks{})
}

// AddCommitHook registers fn to run when the transaction enclosing ctx
// commits. When ctx carries no collector — the auto-commit path — there
// is no commit boundary to wait for and fn runs immediately.
func AddCommitHook(ctx context.Context, fn func()) {
	if fn == nil {
		return
	}
	if c, ok := ctx.Value(commitHookKey{}).(*commitHooks); ok {
		c.mu.Lock()
		c.fns = append(c.fns, fn)
		c.mu.Unlock()
		return
	}
	fn()
}

// HasCommitHooks reports whether ctx carries a collector, i.e. whether
// it descends from an [InTx] attempt.
//
// Code that writes inside a caller-supplied *sql.Tx uses this to tell
// the two transaction styles apart: with a collector there is a commit
// boundary to defer fan-out to, without one the transaction was opened
// by hand and nothing will ever tell the fan-out that it committed.
func HasCommitHooks(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(commitHookKey{}).(*commitHooks)
	return ok
}

// runCommitHooks fires and clears every callback registered on ctx's
// collector. Called by [InTx] only after a successful commit.
func runCommitHooks(ctx context.Context) {
	c, ok := ctx.Value(commitHookKey{}).(*commitHooks)
	if !ok {
		return
	}
	c.mu.Lock()
	fns := c.fns
	c.fns = nil
	c.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
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
					slog.Int("attempt", attempt))
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
			slog.Int("attempt", attempt),
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
		slog.Int("attempts", MaxAttempts),
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
// committed row should be registered with [AddCommitHook] on the context
// fn receives; InTx runs those callbacks only after the commit succeeds
// and drops them when an attempt rolls back, so a retried or aborted
// transaction never leaks a spurious event.
//
// opts is forwarded to BeginTx untouched. Pass nil for the default
// isolation level.
func InTx(ctx context.Context, db TxBeginner, label string, opts *sql.TxOptions, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return Do(ctx, label, func(ctx context.Context) error {
		// Fresh collector per attempt: hooks registered by an attempt
		// that later rolls back are abandoned.
		ctx = WithCommitHooks(ctx)
		tx, err := db.BeginTx(ctx, opts)
		if err != nil {
			return err
		}
		// Rollback on every path that does not commit. Calling Rollback
		// after a successful Commit is a no-op the driver swallows
		// (sql.ErrTxDone), so the deferred cleanup is unconditional.
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		// Commit succeeded and locks are released; fire the deferred
		// fan-out now so it observes the committed rows without
		// contending on this transaction's locks.
		runCommitHooks(ctx)
		return nil
	})
}
