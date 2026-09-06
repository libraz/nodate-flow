// Package events contains Huma operation handlers for the
// /workspaces/{wsId}/events/* endpoints. Today the only operation is
// the reversal flow (POST .../events/{eventPublicId}/reverse) per
// ADR 0008 D4 — listing events lives in the timeline package and is
// scoped per task / project / workspace there.
package events

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Mutations records the reversal in the audit log an administrator
	// queries by action name. Reversing an event is an administrative act
	// performed on somebody else's record, so "who undid this" has to be
	// answerable from that table and not only from the compensating event
	// row; mutation_static_test.go is what keeps a later handler from
	// recording one half without the other.
	Mutations *mutationlog.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// ReverseInput is the request shape for
// POST /workspaces/{wsId}/events/{eventPublicId}/reverse. The body is
// intentionally empty: the reversal target is identified entirely by
// the path so a stray body cannot accidentally widen the operation.
type ReverseInput struct {
	WsID          string `path:"wsId"`
	EventPublicID string `path:"eventPublicId" doc:"Event public id (UUID v7) of the LLM-origin event to reverse"`
}

// ReverseOutputBody is the response payload echoed back on a
// successful reversal. The new compensating event's public_id and
// occurred_at are the only useful pieces of metadata the caller needs
// to wire follow-up UI without a second round-trip.
type ReverseOutputBody struct {
	PublicID   string `json:"publicId" doc:"Public id (UUID v7) of the newly appended compensating event"`
	OccurredAt int64  `json:"occurredAt" doc:"Unix seconds (UTC) the compensating event was stamped with"`
}

// ReverseOutput is the response for POST .../events/{eventPublicId}/reverse.
// Status is set to 201 to mirror create-shaped POSTs: the operation
// produces a brand-new compensating event row in the immutable log,
// not a state mutation on the target row (which stays untouched per
// CLAUDE.md rule 10 + ADR 0008 D4 — events are immutable, reversals
// are new rows that the projection cancels out).
type ReverseOutput struct {
	Status int `header:"Status,omitempty"`
	Body   ReverseOutputBody
}
