package ai

import (
	"context"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlquery"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// CompileLensInput is the POST /workspaces/{wsId}/ai/compile-lens body.
// The client only sends prose and the path workspace id; field names
// and operators come entirely from the server-side whitelist (ADR
// 0004 §5).
type CompileLensInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Prompt string `json:"prompt" minLength:"1" maxLength:"500"`
	}
}

// CompileLensOutput wraps the validated Lens plus the prompt that
// produced it (echoed for client-side undo / save-as-lens flows).
type CompileLensOutput struct {
	Body struct {
		Prompt string        `json:"prompt"`
		Lens   *nlquery.Lens `json:"lens"`
	}
}

// CompileLens handles POST /workspaces/{wsId}/ai/compile-lens. It runs
// the NL query compiler under the workspace's LLM provider (or the
// mock under NF_AI_MOCK=1) and returns the validated Lens JSON.
// Failures surface as AI.NL_QUERY.UNPARSEABLE so the glass dock can
// render a "rephrase" toast.
func CompileLens(deps Deps) func(context.Context, *CompileLensInput) (*CompileLensOutput, error) {
	return func(ctx context.Context, in *CompileLensInput) (*CompileLensOutput, error) {
		if _, ok := middleware.WorkspaceFromContext(ctx); !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if deps.NlQuery == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}
		lens, err := deps.NlQuery.Compile(ctx, in.Body.Prompt)
		if err != nil {
			if errors.Is(err, nlquery.ErrUnparseable) {
				return nil, httpErr(apierrors.AiNlQueryUnparseable)
			}
			return nil, httpErr(apierrors.AiProviderUpstreamCallFailed)
		}
		out := &CompileLensOutput{}
		out.Body.Prompt = in.Body.Prompt
		out.Body.Lens = lens
		return out, nil
	}
}
