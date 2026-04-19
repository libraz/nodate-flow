package nlconstraint

import (
	"context"
	"fmt"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// compileConstraintSystem is the system prompt sent to the LLM when
// compiling natural-language prose into a constraint DSL expression.
// The closed op / field set is repeated verbatim so the model cannot
// hallucinate ops that constraint.Parse would reject.
const compileConstraintSystem = `You are a constraint compiler for nodate-flow.
Given a natural-language description of a task constraint, return ONLY a JSON object
matching the constraint DSL.

Allowed ops (the "op" field):
- "and"                — logical AND; requires "terms" (array of constraints)
- "or"                 — logical OR; requires "terms" (array of constraints)
- "not"                — logical NOT; requires "term" (single constraint)
- "time.due_before"    — task must be due before a date; requires "arg" (YYYY-MM-DD)
- "time.due_after"     — task must be due after a date; requires "arg" (YYYY-MM-DD)
- "dependency.all_done" — all listed tasks must be done; requires "taskIds" (string array)
- "dependency.open_at_most" — at most N dependencies may be open; requires "max" (int >= 0)
- "actor.has_role"     — a specific role must be assigned; requires "arg" (role name)
- "signal.received"    — a named signal must have been received; requires "arg"
- "approval.granted"   — a named approval must have been granted; requires "arg"
- "ci.status_is"       — CI pipeline must report a status; requires "arg" (e.g. "success")

Rules:
- Return ONLY valid JSON, no prose, no markdown fences.
- Use only ops from the allowed list above.
- Combine conditions with "and" / "or" / "not".
- Do NOT invent ops or fields outside the allowed set.`

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
// LLM provider. It sends the constraint-compilation system prompt and
// returns the raw response text for constraint.Parse to verify.
type WorkspaceProvider struct {
	Resolver  WorkspaceProviderResolver
	ExtractWS WorkspaceIDFromContext
}

// NewWorkspaceProvider constructs a WorkspaceProvider. Both arguments
// are required.
func NewWorkspaceProvider(r WorkspaceProviderResolver, extractWS WorkspaceIDFromContext) *WorkspaceProvider {
	if r == nil {
		panic("nlconstraint.NewWorkspaceProvider: resolver must be non-nil")
	}
	if extractWS == nil {
		panic("nlconstraint.NewWorkspaceProvider: extractWS must be non-nil")
	}
	return &WorkspaceProvider{Resolver: r, ExtractWS: extractWS}
}

// CompileConstraint implements Provider. It resolves the workspace's
// default LLM provider from the request context, sends the system
// prompt + user prose, and returns the raw response bytes.
func (w *WorkspaceProvider) CompileConstraint(ctx context.Context, prompt string) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlconstraint: workspace ID not found in context")
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlconstraint: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlconstraint: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	resp, err := prov.Complete(ctx, providers.Request{
		System:    compileConstraintSystem,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("nlconstraint: provider call failed: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnparseable
	}
	return []byte(text), nil
}
