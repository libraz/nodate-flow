package signals

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterCollection wires POST /signals. The caller must attach
// RequireAuth to the underlying chi router.
func RegisterCollection(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "signals-create",
		Method:      http.MethodPost,
		Path:        "/signals",
		Summary:     "Manually inject a signal",
	}, Create(deps))
}
