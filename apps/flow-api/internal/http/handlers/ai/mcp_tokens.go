package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
)

// mcpTokenDisplayPrefixLen is the number of leading plaintext characters
// stored in mcp_tokens.token_prefix for masked display. The column is
// CHAR(8); we store the first 8 characters of the plaintext (which always
// begin with "mcp_") so users can identify their tokens at a glance.
const mcpTokenDisplayPrefixLen = 8

// CreateMcpToken handles POST /workspaces/{wsId}/me/mcp-tokens.
//
// The plaintext token is returned exactly once in the response and never
// stored or logged. Only the SHA-256 hash is persisted.
func CreateMcpToken(deps Deps) func(context.Context, *CreateMcpTokenInput) (*CreateMcpTokenOutput, error) {
	return func(ctx context.Context, in *CreateMcpTokenInput) (*CreateMcpTokenOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		userID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		if in.Body.Scopes == nil {
			in.Body.Scopes = []string{}
		}
		// Reject any requested scope outside the supported allowlist so a
		// token can never carry free-text, unmatched (dead) scopes. The
		// allowlist is shared with the MCP scope gate (mcp.SupportedScopes)
		// so issuance and enforcement cannot drift.
		for _, sc := range in.Body.Scopes {
			if !mcp.IsSupportedScope(sc) {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
		}
		scopesJSON, err := json.Marshal(in.Body.Scopes)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		plain, hash, err := auth.GenerateMCP()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		displayPrefix := plain
		if len(displayPrefix) > mcpTokenDisplayPrefixLen {
			displayPrefix = displayPrefix[:mcpTokenDisplayPrefixLen]
		}

		var agentID sql.NullInt32
		if in.Body.AgentID != nil && *in.Body.AgentID != "" {
			agentPub, perr := types.Parse(*in.Body.AgentID)
			if perr != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			id, qerr := deps.Queries.FindAgentInternalIDByPublicID(ctx, generated.FindAgentInternalIDByPublicIDParams{
				WorkspaceID: ws.ID,
				PublicID:    agentPub,
			})
			if qerr != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			agentID = sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- agent_id is agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		pub := types.New()
		now := time.Now()

		// An expiry in the past would mint a token that is dead on
		// arrival, and the caller would not find out until the first tool
		// call failed with MCP.TOKEN.EXPIRED.
		var expiresAt sql.NullTime
		if in.Body.ExpiresAt != nil {
			exp := time.Unix(*in.Body.ExpiresAt, 0).UTC()
			if !exp.After(now) {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			expiresAt = sql.NullTime{Time: exp, Valid: true}
		}

		if _, err := deps.Queries.CreateMcpToken(ctx, generated.CreateMcpTokenParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			UserID:      userID,
			AgentID:     agentID,
			Name:        in.Body.Name,
			TokenHash:   hash,
			TokenPrefix: displayPrefix,
			ScopesJson:  scopesJSON,
			ExpiresAt:   expiresAt,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &CreateMcpTokenOutput{}
		out.Body.ID = pub.String()
		out.Body.Name = in.Body.Name
		out.Body.Token = plain
		out.Body.TokenPrefix = displayPrefix
		out.Body.Scopes = in.Body.Scopes
		if in.Body.AgentID != nil && *in.Body.AgentID != "" {
			v := *in.Body.AgentID
			out.Body.AgentID = &v
		}
		if expiresAt.Valid {
			v := expiresAt.Time.Unix()
			out.Body.ExpiresAt = &v
		}
		out.Body.CreatedAt = now.Unix()
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "mcp_token.create",
			ActorID:      userID,
			WorkspaceID:  ws.ID,
			ResourceType: "mcp_token",
			ResourceID:   pub.String(),
		})
		// Drop the plaintext from the local variable as soon as it has
		// been copied into the response struct.
		plain = ""
		_ = plain
		return out, nil
	}
}

// ListMcpTokens handles GET /workspaces/{wsId}/me/mcp-tokens.
func ListMcpTokens(deps Deps) func(context.Context, *ListMcpTokensInput) (*ListMcpTokensOutput, error) {
	return func(ctx context.Context, in *ListMcpTokensInput) (*ListMcpTokensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		userID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListMcpTokensForUser(ctx, generated.ListMcpTokensForUserParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListMcpTokensOutput{}
		out.Body.Tokens = make([]McpTokenSummary, 0, len(rows))
		for _, r := range rows {
			scopes := []string{}
			_ = json.Unmarshal(r.ScopesJson, &scopes)
			var agentIDStr *string
			if r.AgentPublicID != (types.PublicID{}) {
				s := r.AgentPublicID.String()
				agentIDStr = &s
			}
			out.Body.Tokens = append(out.Body.Tokens, McpTokenSummary{
				ID:          r.PublicID.String(),
				Name:        r.Name,
				TokenPrefix: r.TokenPrefix,
				Scopes:      scopes,
				AgentID:     agentIDStr,
				ExpiresAt:   nullTimeUnix(r.ExpiresAt),
				LastUsedAt:  nullTimeUnix(r.LastUsedAt),
				CreatedAt:   r.CreatedAt.Unix(),
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// DeleteMcpToken handles DELETE /workspaces/{wsId}/me/mcp-tokens/{tokenId}.
func DeleteMcpToken(deps Deps) func(context.Context, *DeleteMcpTokenInput) (*DeleteMcpTokenOutput, error) {
	return func(ctx context.Context, in *DeleteMcpTokenInput) (*DeleteMcpTokenOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		userID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		pub, err := types.Parse(in.TokenID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		// Scoped to this user's live tokens in this workspace, so a zero
		// count means nothing was revoked. This is the endpoint the audit
		// item is named for: revoking a token id that does not exist -- the
		// sort of thing that happens mid-incident, from a stale list --
		// returned ok and wrote an audit entry saying the token was killed,
		// while the token that was actually leaked stayed valid.
		rows, err := deps.Queries.RevokeMcpToken(ctx, generated.RevokeMcpTokenParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.McpTokenNotFound)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "mcp_token.delete",
			ActorID:      userID,
			WorkspaceID:  ws.ID,
			ResourceType: "mcp_token",
			ResourceID:   pub.String(),
		})
		out := &DeleteMcpTokenOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
