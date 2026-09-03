// Package dbretry re-exports the shared retry policy and commit-boundary
// types from packages/go-shared/dbretry under flow-api's own import
// path.
//
// Everything lives in the shared package because both event appenders
// have to agree on it: flow-api's eventbus and the cross-service
// eventlog both defer their fan-out to the same commit boundary, and a
// second boundary type declared here would be a type neither appender
// accepts. These are aliases, not new types, so the call sites stay
// unchanged and the identity is exact.
//
// The fan-out path is the primary caller: parallel goroutines append
// rows to the events log and create notification rows for the same
// workspace, so FK record locks on parents (workspaces, tasks, users)
// race with cleanup paths (PurgeWorkspace) under heavy parallel test
// load. Rather than tightening the lock graph everywhere, the pragmatic
// standard MySQL recipe is to retry deadlocks at the call site that
// experiences them.
package dbretry

import (
	shared "github.com/libraz/nodate-flow/packages/go-shared/dbretry"
)

// MaxAttempts is the upper bound on retry rounds (initial attempt +
// retries), shared with auth-api.
const MaxAttempts = shared.MaxAttempts

// TxBeginner is the subset of *sql.DB needed by [InTx].
type TxBeginner = shared.TxBeginner

// IsTransient reports whether err is a MySQL deadlock or
// lock-wait-timeout that should be retried by the caller.
var IsTransient = shared.IsTransient

// Do runs fn, retrying up to [MaxAttempts] when fn returns a transient
// MySQL error (deadlock or lock-wait-timeout).
var Do = shared.Do

// InTx runs fn inside a fresh transaction, retrying the whole
// transaction (begin → fn → commit) on transient MySQL errors and
// running the hooks registered with [Tx.AfterCommit] once it commits.
var InTx = shared.InTx

// CommitBoundary is a database handle that knows when its writes become
// durable. Only [Tx] and [AutoCommitDB] satisfy it.
type CommitBoundary = shared.CommitBoundary

// Tx is the transaction [InTx] hands to its closure, carrying the
// collector for work that must wait for the commit.
type Tx = shared.Tx

// AutoCommitDB is a *sql.DB addressed as a commit boundary: each
// statement commits on its own and post-commit work runs immediately.
type AutoCommitDB = shared.AutoCommitDB

// AutoCommit declares that statements issued through db commit
// individually, with no enclosing transaction to wait for.
var AutoCommit = shared.AutoCommit
