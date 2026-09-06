// Package unrecorded registers a write operation that changes a row and
// records nothing. It differs from the recorded fixture only in that
// one respect.
package unrecorded

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
		OperationID: "thing-delete",
		Method:      http.MethodDelete,
		Path:        "/things/{id}",
	}, Delete(deps))
}

func List(deps Deps) handler { return func() { load(deps) } }

func Delete(deps Deps) handler {
	return func() {
		deps.Queries.DisableThing(ctx, id)
	}
}
