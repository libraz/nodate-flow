package ai

import (
	"context"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ResolveCommandInput is the POST /workspaces/{wsId}/ai/resolve-command body.
// The client sends free-form prose and receives a validated MCP tool call.
type ResolveCommandInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Prompt string `json:"prompt" minLength:"1" maxLength:"500" doc:"Natural language command"`
	}
}

// ResolveCommandOutput wraps the resolved MCP tool call with its confidence
// score so the command palette can decide whether to auto-execute or confirm.
type ResolveCommandOutput struct {
	Body struct {
		Tool       string         `json:"tool" doc:"Resolved MCP tool name"`
		Args       map[string]any `json:"args" doc:"Tool arguments"`
		Confidence float64        `json:"confidence" doc:"Confidence score (0.0 to 1.0)"`
	}
}

// ResolveCommand handles POST /workspaces/{wsId}/ai/resolve-command. It runs
// the NL command resolver under the workspace's LLM provider (or the mock
// under NF_AI_MOCK=1) and returns the validated ToolCall JSON. Failures
// surface as AI.RESPONSE.INVALID_JSON so the command palette can render
// a "could not understand" toast.
func ResolveCommand(deps Deps) func(context.Context, *ResolveCommandInput) (*ResolveCommandOutput, error) {
	return func(ctx context.Context, in *ResolveCommandInput) (*ResolveCommandOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if deps.NlCommand == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}

		tc, err := deps.NlCommand.Resolve(ctx, in.Body.Prompt)
		if err != nil {
			if errors.Is(err, nlcommand.ErrBudgetExceeded) {
				return nil, httpErr(apierrors.AiCostGuardExceeded)
			}
			if errors.Is(err, nlcommand.ErrUnresolvable) {
				return nil, httpErr(apierrors.AiResponseInvalidJson)
			}
			return nil, mapProviderError(err)
		}

		// Audit the resolved command so usage analytics and cost tracking
		// can attribute NL→MCP invocations back to users.
		if actorID, aok := middleware.ActorFromContext(ctx); aok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_command.resolve",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_command",
				Metadata: map[string]any{
					"tool": tc.Tool,
				},
			})
		}

		out := &ResolveCommandOutput{}
		out.Body.Tool = tc.Tool
		out.Body.Args = tc.Args
		out.Body.Confidence = tc.Confidence
		return out, nil
	}
}
