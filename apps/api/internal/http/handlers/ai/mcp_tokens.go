package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
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

		pub := types.New()
		now := time.Now()
		if _, err := deps.Queries.CreateMcpToken(ctx, generated.CreateMcpTokenParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			UserID:      userID,
			Name:        in.Body.Name,
			TokenHash:   hash,
			TokenPrefix: displayPrefix,
			ScopesJson:  scopesJSON,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &CreateMcpTokenOutput{}
		out.Body.ID = pub.String()
		out.Body.Name = in.Body.Name
		out.Body.Token = plain
		out.Body.TokenPrefix = displayPrefix
		out.Body.Scopes = in.Body.Scopes
		out.Body.CreatedAt = now.Unix()
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
			out.Body.Tokens = append(out.Body.Tokens, McpTokenSummary{
				ID:          r.PublicID.String(),
				Name:        r.Name,
				TokenPrefix: r.TokenPrefix,
				Scopes:      scopes,
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
		if err := deps.Queries.RevokeMcpToken(ctx, generated.RevokeMcpTokenParams{
			WorkspaceID: ws.ID,
			UserID:      userID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &DeleteMcpTokenOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
