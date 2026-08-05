package internalapi

import (
	"context"
	"database/sql"
	"errors"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// ByDiscord handles GET /internal/users/by-discord/{snowflake}. The
// route is mounted behind RequireSignalsAuth so the only authentication
// mode is the shared service-token bearer; JWT / PAT / MCP callers
// receive 401 from the middleware before this function runs.
//
// Resolution proceeds in two steps:
//
//  1. The snowflake is matched against user_integrations.metadata_json
//     $.external_user_id for an enabled discord-provider row. The
//     pattern constraint on the path parameter already rejects
//     non-numeric inputs with VALIDATION.PATH.FIELD_INVALID, so this
//     handler trusts the input shape.
//  2. The matched user's earliest-joined enabled workspace_members
//     row decides the workspace scope. There is no
//     users.default_workspace_id column today (v1.0 limitation), so the
//     gateway emits to whichever workspace the user joined first; this
//     ties signals to a single workspace for that user even when they
//     belong to several.
//
// A 404 with INTEGRATION.DISCORD.USER_NOT_FOUND covers both "no
// matching integration" and "integration is disabled" so the absence of
// the binding is not externally distinguishable from a soft-disabled
// row. Any other database error collapses to INTERNAL.UNEXPECTED so the
// presence-discord gateway treats it as signal_failed rather than
// drop_no_user.
func ByDiscord(deps Deps) func(context.Context, *ByDiscordInput) (*ByDiscordOutput, error) {
	return func(ctx context.Context, in *ByDiscordInput) (*ByDiscordOutput, error) {
		row, err := deps.Queries.FindUserByDiscordSnowflake(ctx, in.Snowflake)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.IntegrationDiscordUserNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &ByDiscordOutput{Body: ByDiscordOutputBody{
			UserID:      row.UserPublicID.String(),
			WorkspaceID: row.WorkspacePublicID.String(),
		}}, nil
	}
}
