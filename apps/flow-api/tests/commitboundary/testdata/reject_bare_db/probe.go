// Package p must not compile. Appending straight through a pool is a
// legitimate thing to do, but it is a decision — the caller has to name
// the auto-commit path with dbretry.AutoCommit rather than reach for
// whichever handle is nearest.
//
// This directory is testdata: the go tool never walks into it, so the
// file is compiled only by the gate in the parent package.
package p

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func appendThroughBarePool(ctx context.Context, db *sql.DB) error {
	return eventbus.Append(ctx, db, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: 7,
	})
}
