// Package p must compile. It is the other half of the gate: the two
// shapes a caller is supposed to write, in the same file layout and
// against the same appender as the refused ones.
//
// It is also the control for the whole check. If this fails to build,
// the refusals next door prove nothing — they would be failing for a
// broken import path or a stale module rather than for the boundary.
//
// This directory is testdata: the go tool never walks into it, so the
// file is compiled only by the gate in the parent package.
package p

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	sharedlog "github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// appendInsideInTx defers the fan-out to the transaction's commit.
func appendInsideInTx(ctx context.Context, db *sql.DB) error {
	return dbretry.InTx(ctx, db, "gate.probe", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: 7,
		})
	})
}

// appendOnAutoCommit says out loud that there is no boundary to wait
// for, which is what makes the immediate fan-out correct.
func appendOnAutoCommit(ctx context.Context, db *sql.DB) error {
	return eventbus.Append(ctx, dbretry.AutoCommit(db), eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: 7,
	})
}

// appendThroughSharedLog covers the second appender: both take the same
// boundary type, so a change that narrowed only one would show up here.
func appendThroughSharedLog(ctx context.Context, db *sql.DB) error {
	_, err := sharedlog.Append(ctx, dbretry.AutoCommit(db), sharedlog.Event{
		Type:        sharedbus.ItemScheduled,
		WorkspaceID: 3,
	})
	return err
}
