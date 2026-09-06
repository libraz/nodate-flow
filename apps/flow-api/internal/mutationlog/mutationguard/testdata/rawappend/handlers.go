// Package rawappend reaches the recorder and also appends an event of
// its own, and holds a bare audit recorder on the side. Both are the
// shape that lets one half of a change be written without the other,
// and both are invisible to a reachability check on its own.
package rawappend

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
)

// eventbus.Append in a comment is not a call and must not be reported.

func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "thing-create",
		Method:      http.MethodPost,
		Path:        "/things",
	}, Create(deps))
}

func Create(deps Deps) handler {
	return func() {
		deps.Mutations.Record(ctx, act, mutationlog.Mutation{
			EventType:    eventbus.ThingCreated,
			AuditAction:  "thing.create",
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "rawappend.Create",
		})
		eventbus.AppendBestEffort(ctx, db, eventbus.Event{}, "rawappend.Create")
		// Taken as a value rather than called, which a textual search
		// finds and a check on call expressions alone does not.
		appendTwice := eventbus.Append
		appendTwice(ctx, db, eventbus.Event{})
		deps.Audit.Record(ctx, audit.Entry{Action: "thing.create"})
	}
}
