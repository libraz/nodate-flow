package nlconstraint

import (
	"context"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// ErrBudgetExceeded is returned by CompileConstraint when the workspace
// has already spent more than its daily LLM budget. The HTTP layer maps
// it to AI.COST_GUARD.EXCEEDED, mirroring the orchestrator's
// ai.ErrDailyBudgetExceeded handling so every NL surface trips the same
// per-workspace cap.
var ErrBudgetExceeded = errors.New("nlconstraint: daily budget exceeded")

// CostGuard is the narrow contract WorkspaceProvider needs to participate
// in the per-workspace daily cap. It mirrors ai.CostGuard.Check so the
// NL-constraint path shares one budget with the orchestrator and
// signal-judge (ADR 0008 D3). A nil guard disables enforcement.
type CostGuard interface {
	Check(ctx context.Context, workspaceID uint32) error
}

// InvocationLogger persists a redacted record of the LLM call. The
// signature matches ai.InvocationLogger so production wiring can pass the
// same logger used by the orchestrator and ai_invocations stays a single,
// uniform audit + cost surface.
type InvocationLogger = ai.InvocationLogger

// InvocationRecord is the redacted ai_invocations payload. Field shape
// mirrors ai.InvocationRecord so callers pass a shared logger without
// translating between two structs.
type InvocationRecord = ai.InvocationRecord

// InvocationMetricsHook aliases [ai.InvocationMetricsHook]; decoded usage is
// reported even when the output is rejected.
type InvocationMetricsHook = ai.InvocationMetricsHook

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
- "signal.received"    — a signal of the named kind must be attached to the task; requires "arg"

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

	// Guard enforces the per-workspace daily spend cap before the LLM
	// call. Nil disables enforcement (MVP / mock paths).
	Guard CostGuard
	// LogInvoke persists a redacted ai_invocations row after the call so
	// NL-constraint spend is tracked, attributable, and counted against
	// the daily budget. Nil disables logging.
	LogInvoke InvocationLogger
	// OnInvocation records the Prometheus cost metric. Nil disables it.
	OnInvocation InvocationMetricsHook
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

// WithMetering wires the cost guard, invocation logger, and metrics hook
// so the NL-constraint path enforces the daily budget and records
// redacted ai_invocations rows like the orchestrator. Returns the
// receiver for fluent construction.
func (w *WorkspaceProvider) WithMetering(guard CostGuard, log InvocationLogger, onInvoke InvocationMetricsHook) *WorkspaceProvider {
	w.Guard = guard
	w.LogInvoke = log
	w.OnInvocation = onInvoke
	return w
}

// CompileConstraint implements Provider. It resolves the workspace's
// default LLM provider from the request context, sends the system
// prompt + user prose, and returns the raw response bytes.
func (w *WorkspaceProvider) CompileConstraint(ctx context.Context, prompt string) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlconstraint: workspace ID not found in context")
	}

	// Enforce the per-workspace daily spend cap before billing the
	// provider, matching the orchestrator (ai/tasks.go) and signal-judge
	// runner. On would-exceed, surface the shared budget sentinel; any
	// other guard error (e.g. the budget reader failing) propagates as-is.
	if capHit, err := ai.CheckBudget(ctx, w.Guard, wsID); err != nil {
		if capHit {
			return nil, ErrBudgetExceeded
		}
		return nil, fmt.Errorf("nlconstraint: cost guard: %w", err)
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlconstraint: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlconstraint: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	req := airequest.New(prov, airequest.Args{
		System:    compileConstraintSystem,
		Prompt:    prompt,
		MaxTokens: 1024,
		Output:    constraintOutputSpec,
	})
	var output []byte
	accounting := &ai.Orchestrator{LogInvoke: w.LogInvoke, OnInvocation: w.OnInvocation}
	_, err = accounting.CompleteAndRecord(ctx, wsID, "compile_constraint", prov, req, func(resp *providers.Response) error {
		var err error
		output, err = validateConstraintOutput(resp)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("nlconstraint: provider call failed: %w", err)
	}
	return output, nil
}
