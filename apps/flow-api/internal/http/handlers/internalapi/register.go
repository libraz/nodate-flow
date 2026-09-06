package internalapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires every operation in the package under the /internal/*
// path prefix. The caller MUST mount this on a chi sub-router whose only
// middleware is middleware.RequireServiceTokenOnly, and the standard JWT
// chain must NOT be present: these endpoints are not meant to be
// reachable with a user bearer at all.
//
// middleware.RequireSignalsAuth is NOT an acceptable substitute despite
// the similar name. It falls through to the JWT chain for any bearer
// that is not the service token, so mounting it here would admit every
// valid user session — and the handlers below carry no membership check,
// because the guard is meant to leave them nothing to check. The
// snowflake lookup would then answer any logged-in user of any workspace
// with another workspace's user and workspace ids.
//
// Every operation here is marked Hidden so it stays fully routable for
// the service-token caller (e.g. presence-discord) while being excluded
// from the generated public OpenAPI document and the TypeScript SDK.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "internal-users-by-discord",
		Method:      http.MethodGet,
		Path:        "/internal/users/by-discord/{snowflake}",
		Summary:     "Resolve a Discord snowflake to a flow user (service-token only)",
		Description: "Returns the flow user public_id and default workspace public_id bound to the supplied Discord snowflake via user_integrations.metadata_json.external_user_id. Service-token only: requests authenticated as a user receive 401 from the middleware. Used by the presence-discord gateway before emitting a discord.presence signal.",
		Tags:        []string{"Internal"},
		Hidden:      true,
	}, ByDiscord(deps))
}
