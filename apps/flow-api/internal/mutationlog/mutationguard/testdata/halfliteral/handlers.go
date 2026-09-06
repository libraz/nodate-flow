// Package halfliteral records through the recorder, so it satisfies
// every reachability and centralisation check, and still describes half
// of each change: one literal names no audit action, one names no event
// kind, and one names an event kind at the entry point that does not
// append it.
package halfliteral

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "thing-create",
		Method:      http.MethodPost,
		Path:        "/things",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "thing-update",
		Method:      http.MethodPatch,
		Path:        "/things/{id}",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "thing-transition",
		Method:      http.MethodPost,
		Path:        "/things/transition",
	}, Transition(deps))
}

func Create(deps Deps) handler {
	return func() {
		deps.Mutations.Record(ctx, act, mutationlog.Mutation{
			EventType:    eventbus.ThingCreated,
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "halfliteral.Create",
		})
	}
}

func Update(deps Deps) handler {
	return func() {
		deps.Mutations.Record(ctx, act, mutationlog.Mutation{
			AuditAction:  "thing.update",
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "halfliteral.Update",
		})
	}
}

func Transition(deps Deps) handler {
	return func() {
		deps.Mutations.RecordTxAudit(ctx, act, mutationlog.Mutation{
			EventType:    eventbus.ThingTransitioned,
			AuditAction:  "thing.transition",
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "halfliteral.Transition",
		})
	}
}
