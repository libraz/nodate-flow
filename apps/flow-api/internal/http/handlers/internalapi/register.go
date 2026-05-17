package internalapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires every operation in the package under the /internal/*
// path prefix. The caller MUST mount this on a chi sub-router whose only
// middleware is RequireSignalsAuth (or an equivalent service-token
// guard); the standard JWT chain must NOT be present, because these
// endpoints are not meant to be reachable with a user bearer at all.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "internal-users-by-discord",
		Method:      http.MethodGet,
		Path:        "/internal/users/by-discord/{snowflake}",
		Summary:     "Resolve a Discord snowflake to a flow user (service-token only)",
		Description: "Returns the flow user public_id and default workspace public_id bound to the supplied Discord snowflake via user_integrations.metadata_json.external_user_id. Service-token only: requests authenticated as a user receive 401 from the middleware. Used by the presence-discord gateway before emitting a discord.presence signal.",
		Tags:        []string{"Internal"},
	}, ByDiscord(deps))
}
