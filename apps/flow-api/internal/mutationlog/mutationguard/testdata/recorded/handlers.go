// Package recorded is the shape the guard must accept: a read
// operation that records nothing, a write that records both halves
// through the recorder, and a write whose event a shared transactional
// helper already appended.
//
// The fixtures under testdata are parsed, never compiled. They exist so
// each check can be shown to fail on a package that breaks the rule and
// pass on one that keeps it — a guard only demonstrated against correct
// code is indistinguishable from one that reports nothing.
package recorded

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "thing-list",
		Method:      http.MethodGet,
		Path:        "/things",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "thing-create",
		Method:      http.MethodPost,
		Path:        "/things",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "thing-transition",
		Method:      http.MethodPost,
		Path:        "/things/transition",
	}, Transition(deps))
}

func List(deps Deps) handler { return func() { load(deps) } }

func Create(deps Deps) handler {
	return func() {
		deps.Mutations.Record(ctx, act, mutationlog.Mutation{
			EventType:    eventbus.ThingCreated,
			AuditAction:  "thing.create",
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "recorded.Create",
		})
	}
}

func Transition(deps Deps) handler {
	return func() {
		applyTransition()
		deps.Mutations.RecordTxAudit(ctx, act, mutationlog.Mutation{
			AuditAction:  "thing.transition",
			ResourceType: "thing",
			ResourceID:   id,
			CallSite:     "recorded.Transition (taskstate.ApplyTransitionTx appended the event)",
		})
	}
}
