package nlquery

import (
	"context"
	"fmt"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// compileLensSystem is the system prompt sent to the LLM when compiling
// natural-language prose into a Lens JSON object. The closed field /
// operator whitelist is repeated verbatim so the model cannot hallucinate
// fields that ValidateBytes would reject.
const compileLensSystem = `You are a task-filter compiler for nodate-flow.
Given a natural-language query, return ONLY a JSON object with this shape:

{
  "filter": { "<field>": { "<op>": <value> } },
  "sort":   [ { "field": "<field>", "dir": "asc"|"desc" } ],
  "groupBy": "<field>" | null
}

Allowed filter fields: title, status, assignee, project, workspace, priority, estimate, created_at, updated_at, due_on, blocked, labels, has_estimate.
Allowed operators: eq, neq, in, nin, gt, gte, lt, lte, contains, between, is_null, is_not_null.
Allowed sort fields: title, status, assignee, project, priority, estimate, created_at, updated_at, due_on.
Allowed groupBy values: status, assignee, project, priority, labels.

Value mappings:
- priority: 1 = low, 2 = medium, 3 = high. "high priority" means priority eq 3, "low priority" means priority eq 1.
- status values: "open", "in_progress", "review", "done", "cancelled".
- blocked: true or false.

Rules:
- Return ONLY valid JSON, no prose, no markdown fences.
- filter must have at least one field.
- "between" accepts a single token string such as "this_week", "last_7_days", "this_month".
- If the query implies a sort, include it; otherwise omit the sort array or leave it empty.
- If the query implies a grouping, set groupBy; otherwise set it to null.
- Do NOT invent fields or operators outside the allowed lists.`

// WorkspaceProviderResolver is the narrow contract WorkspaceProvider needs
// to obtain a providers.Provider for a given workspace. In production this
// is satisfied by ai.ProviderResolver.
type WorkspaceProviderResolver interface {
	Default(ctx context.Context, workspaceID uint32) (providers.Provider, error)
}

// WorkspaceIDFromContext extracts the internal workspace ID from the
// request context. The middleware stores this value before the handler
// runs.
type WorkspaceIDFromContext func(ctx context.Context) (uint32, bool)

// WorkspaceProvider is a Provider backed by the workspace's configured
// LLM provider (via the Orchestrator's ProviderResolver). It calls
// providers.Provider.Complete with a Lens-compilation system prompt and
// returns the raw response text for ValidateBytes to verify.
type WorkspaceProvider struct {
	Resolver  WorkspaceProviderResolver
	ExtractWS WorkspaceIDFromContext
}

// NewWorkspaceProvider constructs a WorkspaceProvider. Both arguments are
// required.
func NewWorkspaceProvider(r WorkspaceProviderResolver, extractWS WorkspaceIDFromContext) *WorkspaceProvider {
	if r == nil {
		panic("nlquery.NewWorkspaceProvider: resolver must be non-nil")
	}
	if extractWS == nil {
		panic("nlquery.NewWorkspaceProvider: extractWS must be non-nil")
	}
	return &WorkspaceProvider{Resolver: r, ExtractWS: extractWS}
}

// CompileLens implements Provider. It resolves the workspace's default LLM
// provider from the request context, sends the system prompt + user prose,
// and returns the raw response bytes.
func (w *WorkspaceProvider) CompileLens(ctx context.Context, prompt string) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlquery: workspace ID not found in context")
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlquery: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlquery: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	resp, err := prov.Complete(ctx, providers.Request{
		System:    compileLensSystem,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("nlquery: provider call failed: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnparseable
	}
	return []byte(text), nil
}
