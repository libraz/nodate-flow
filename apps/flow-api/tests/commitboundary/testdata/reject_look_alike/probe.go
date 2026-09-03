// Package p must not compile. It implements every exported method of
// dbretry.CommitBoundary and is still refused, because the interface is
// sealed by an unexported method only dbretry can supply.
//
// This is the assertion a refactor is most likely to break without
// noticing: dropping the seal, or exporting it, leaves every other test
// in the tree green while re-opening the hole the type was built to
// close.
//
// This directory is testdata: the go tool never walks into it, so the
// file is compiled only by the gate in the parent package.
package p

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// lookAlike carries the full exported method set of dbretry.CommitBoundary.
type lookAlike struct{ tx *sql.Tx }

func (l lookAlike) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return l.tx.ExecContext(ctx, q, a...)
}

func (l lookAlike) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return l.tx.PrepareContext(ctx, q)
}

func (l lookAlike) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return l.tx.QueryContext(ctx, q, a...)
}

func (l lookAlike) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return l.tx.QueryRowContext(ctx, q, a...)
}

func (l lookAlike) AfterCommit(fn func()) { fn() }

func (l lookAlike) RunStatement(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

func (l lookAlike) Fail(error) {}

// The declaration and the call are both refused; either one alone would
// be enough, and both are here so the gate sees the seal hold at an
// assignment as well as at a call site.
var _ dbretry.CommitBoundary = lookAlike{}

func appendThroughLookAlike(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return eventbus.Append(ctx, lookAlike{tx: tx}, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: 7,
	})
}
