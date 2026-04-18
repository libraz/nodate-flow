package ai

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListInvocationsInput is the query input for
// GET /workspaces/{wsId}/ai/invocations.
type ListInvocationsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// AiInvocation is the masked DTO for an ai_invocations row. Prompt and
// response bodies are already redacted at write time; this endpoint
// only forwards what the orchestrator persisted.
type AiInvocation struct {
	ID               string  `json:"id"`
	Purpose          string  `json:"purpose"`
	Model            string  `json:"model"`
	PromptRedacted   string  `json:"promptRedacted"`
	ResponseRedacted string  `json:"responseRedacted,omitempty"`
	TokensInput      int32   `json:"tokensInput,omitempty"`
	TokensOutput     int32   `json:"tokensOutput,omitempty"`
	CostEstimate     string  `json:"costEstimate,omitempty"`
	Status           string  `json:"status"`
	ErrorCode        string  `json:"errorCode,omitempty"`
	InvokedAt        int64   `json:"invokedAt"`
}

// ListInvocationsOutput wraps the paginated invocation list.
type ListInvocationsOutput struct {
	Body struct {
		Invocations []AiInvocation `json:"invocations"`
	}
}

// ListInvocations handles GET /workspaces/{wsId}/ai/invocations. It
// returns the most recent redacted ai_invocations rows for the
// workspace so the AI reasoning / activity panel can render them.
// Workspace-member auth is enforced by the surrounding middleware.
func ListInvocations(deps Deps) func(context.Context, *ListInvocationsInput) (*ListInvocationsOutput, error) {
	return func(ctx context.Context, in *ListInvocationsInput) (*ListInvocationsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListAiInvocationsForWorkspace(ctx, generated.ListAiInvocationsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListInvocationsOutput{}
		out.Body.Invocations = make([]AiInvocation, 0, len(rows))
		for _, r := range rows {
			dto := AiInvocation{
				ID:             r.PublicID.String(),
				Purpose:        r.Purpose,
				Model:          r.Model,
				PromptRedacted: r.PromptRedacted,
				Status:         string(r.Status),
				InvokedAt:      r.InvokedAt.Unix(),
			}
			if r.ResponseRedacted.Valid {
				dto.ResponseRedacted = r.ResponseRedacted.String
			}
			if r.TokensInput.Valid {
				dto.TokensInput = r.TokensInput.Int32
			}
			if r.TokensOutput.Valid {
				dto.TokensOutput = r.TokensOutput.Int32
			}
			if r.CostEstimate.Valid {
				dto.CostEstimate = r.CostEstimate.String
			}
			if r.ErrorCode.Valid {
				dto.ErrorCode = r.ErrorCode.String
			}
			out.Body.Invocations = append(out.Body.Invocations, dto)
		}
		return out, nil
	}
}
