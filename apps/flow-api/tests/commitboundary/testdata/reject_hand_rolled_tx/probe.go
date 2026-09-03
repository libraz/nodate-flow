// Package p must not compile. A transaction opened by hand has no
// commit boundary anyone can observe, so the fan-out an append triggers
// would have nothing to wait for.
//
// This directory is testdata: the go tool never walks into it, so the
// file is compiled only by the gate in the parent package.
package p

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func appendInHandRolledTx(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: 7,
	})
}
