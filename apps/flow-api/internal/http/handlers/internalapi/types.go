// Package internalapi exposes service-token-only endpoints under the
// /internal/* path prefix. The endpoints in this package are not part of
// the public web SDK contract and are mounted behind RequireSignalsAuth
// so that only callers presenting the configured shared secret can
// reach them. The current consumers are:
//
//   - presence-discord (apps/presence-discord) — resolves a Discord
//     snowflake to a flow user + default workspace before emitting a
//     signal.
//
// Adding a new endpoint here requires updating the router's /internal
// sub-router group rather than the standard authenticated chain; see
// apps/flow-api/internal/http/router/router.go.
package internalapi

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to every handler in this
// package. The handlers do not touch storage / email / audit, so the
// bundle is intentionally narrow — adding a field here is a request
// for an additional capability that should be reviewed.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

// httpErr delegates to handlerutil.HTTPErr so the package can return
// the canonical RFC 9457 problem+json envelope without each handler
// importing the helper directly. Mirrors the pattern used by sibling
// handler packages (signals, intake, etc.).
var httpErr = handlerutil.HTTPErr

// ByDiscordInput is the path-parameter envelope for
// GET /internal/users/by-discord/{snowflake}. The snowflake is a
// Discord-issued numeric string of 17-19 digits in practice; the pattern
// constraint is intentionally loose enough to admit future-length
// snowflakes (Discord has reserved bits in the format) while still
// rejecting non-numeric inputs that could only be a caller bug.
type ByDiscordInput struct {
	Snowflake string `path:"snowflake" minLength:"1" maxLength:"32" pattern:"^[0-9]{1,32}$" doc:"Discord user snowflake (numeric string, 17-19 digits in practice)."`
}

// ByDiscordOutputBody is the JSON shape returned by
// GET /internal/users/by-discord/{snowflake}. The fields are the
// resolved flow user public id (UUID v7) and the user's default
// workspace public id (currently the earliest-joined membership; see
// the SQL FindUserByDiscordSnowflake docstring). The presence-discord
// gateway parses this exact shape in
// apps/presence-discord/internal/gateway/emitter.go.
type ByDiscordOutputBody struct {
	UserID      string `json:"userId" doc:"Flow user public_id (UUID v7) bound to the requested Discord snowflake."`
	WorkspaceID string `json:"workspaceId" doc:"Default workspace public_id (UUID v7) for the resolved user. Currently the earliest-joined enabled membership."`
}

// ByDiscordOutput is the Huma output envelope.
type ByDiscordOutput struct {
	Body ByDiscordOutputBody
}
