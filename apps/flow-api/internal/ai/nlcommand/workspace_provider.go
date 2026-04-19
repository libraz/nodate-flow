package nlcommand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// resolveCommandSystem is the system prompt sent to the LLM when
// resolving natural-language commands into MCP tool invocations. The
// tool list is injected dynamically so the model sees only the tools
// actually available.
const resolveCommandSystem = `You are a command resolver for nodate-flow.
Given a natural-language command, resolve it to a single MCP tool invocation.
Return ONLY a JSON object with this shape:

{
  "tool": "<tool_name>",
  "args": { ... },
  "confidence": <0.0-1.0>
}

Rules:
- Return ONLY valid JSON, no prose, no markdown fences.
- "tool" must be one of the tools listed below.
- "args" must match the tool's input schema.
- "confidence" is your estimate of how well the command matches the tool (0.0-1.0).
- If the command is ambiguous, pick the best match but lower the confidence.
- If no tool matches at all, return {"tool":"","args":{},"confidence":0.0}.

Available tools:
%s`

// WorkspaceProviderResolver is the narrow contract WorkspaceProvider
// needs to obtain a providers.Provider for a given workspace. In
// production this is satisfied by providers.WorkspaceResolver.
type WorkspaceProviderResolver interface {
	Default(ctx context.Context, workspaceID uint32) (providers.Provider, error)
}

// WorkspaceIDFromContext extracts the internal workspace ID from the
// request context. The middleware stores this value before the handler
// runs.
type WorkspaceIDFromContext func(ctx context.Context) (uint32, bool)

// WorkspaceProvider is a Provider backed by the workspace's configured
// LLM provider. It sends the command-resolution system prompt with the
// tool catalogue and returns the raw response text.
type WorkspaceProvider struct {
	Resolver  WorkspaceProviderResolver
	ExtractWS WorkspaceIDFromContext
}

// NewWorkspaceProvider constructs a WorkspaceProvider. Both arguments
// are required.
func NewWorkspaceProvider(r WorkspaceProviderResolver, extractWS WorkspaceIDFromContext) *WorkspaceProvider {
	if r == nil {
		panic("nlcommand.NewWorkspaceProvider: resolver must be non-nil")
	}
	if extractWS == nil {
		panic("nlcommand.NewWorkspaceProvider: extractWS must be non-nil")
	}
	return &WorkspaceProvider{Resolver: r, ExtractWS: extractWS}
}

// ResolveCommand implements Provider. It resolves the workspace's
// default LLM provider, builds a system prompt that includes the tool
// catalogue, and returns the raw response bytes.
func (w *WorkspaceProvider) ResolveCommand(ctx context.Context, prompt string, tools []ToolSpec) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlcommand: workspace ID not found in context")
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlcommand: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlcommand: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	// Build tool catalogue for the system prompt.
	toolsJSON, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("nlcommand: marshal tools: %w", err)
	}
	system := fmt.Sprintf(resolveCommandSystem, string(toolsJSON))

	resp, err := prov.Complete(ctx, providers.Request{
		System:    system,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("nlcommand: provider call failed: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnresolvable
	}
	return []byte(text), nil
}
