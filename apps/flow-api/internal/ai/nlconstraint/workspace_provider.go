package nlconstraint

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
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
type InvocationLogger func(ctx context.Context, rec InvocationRecord)

// InvocationRecord is the redacted ai_invocations payload. Field shape
// mirrors ai.InvocationRecord so callers pass a shared logger without
// translating between two structs.
type InvocationRecord struct {
	WorkspaceID      uint32
	Purpose          string
	Model            string
	PromptRedacted   string
	ResponseRedacted string
	TokensInput      int
	TokensOutput     int
	CostCents        int64
	Status           string
	ErrorCode        string
}

// InvocationMetricsHook is called after each LLM provider call. Same
// shape as ai.InvocationMetricsHook so obs.RecordAIInvocation is reused
// without an adapter.
type InvocationMetricsHook func(provider, model, workspaceID string, costCents int64)

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

// logSuccess records the redacted invocation + cost metric after a
// successful provider call.
func (w *WorkspaceProvider) logSuccess(ctx context.Context, wsID uint32, model, wsIDStr, prompt string, resp *providers.Response, kind string) {
	if w.OnInvocation != nil {
		w.OnInvocation(kind, model, wsIDStr, resp.CostCents)
	}
	if w.LogInvoke != nil {
		w.LogInvoke(ctx, InvocationRecord{
			WorkspaceID:      wsID,
			Purpose:          "compile_constraint",
			Model:            model,
			PromptRedacted:   prompt,
			ResponseRedacted: logutil.Redact(resp.Text),
			TokensInput:      resp.InputTokens,
			TokensOutput:     resp.OutputTokens,
			CostCents:        resp.CostCents,
			Status:           "ok",
		})
	}
}

// logFailure records the redacted invocation + zero-cost metric after a
// failed provider call.
func (w *WorkspaceProvider) logFailure(ctx context.Context, wsID uint32, model, wsIDStr, prompt string, callErr error, kind string) {
	if w.OnInvocation != nil {
		w.OnInvocation(kind, model, wsIDStr, 0)
	}
	if w.LogInvoke != nil {
		w.LogInvoke(ctx, InvocationRecord{
			WorkspaceID:    wsID,
			Purpose:        "compile_constraint",
			Model:          model,
			PromptRedacted: prompt,
			Status:         "error",
			ErrorCode:      logutil.Redact(callErr.Error()),
		})
	}
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
	if w.Guard != nil {
		if err := w.Guard.Check(ctx, wsID); err != nil {
			if errors.Is(err, ai.ErrDailyBudgetExceeded) {
				return nil, ErrBudgetExceeded
			}
			return nil, fmt.Errorf("nlconstraint: cost guard: %w", err)
		}
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlconstraint: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlconstraint: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	req := providers.Request{
		System:    compileConstraintSystem,
		Prompt:    prompt,
		MaxTokens: 1024,
	}
	wsIDStr := strconv.FormatUint(uint64(wsID), 10)
	promptRedacted := logutil.Redact(strings.TrimSpace(req.System + "\n" + req.Prompt))
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		w.logFailure(ctx, wsID, req.Model, wsIDStr, promptRedacted, err, string(prov.Kind()))
		return nil, fmt.Errorf("nlconstraint: provider call failed: %w", err)
	}
	w.logSuccess(ctx, wsID, req.Model, wsIDStr, promptRedacted, resp, string(prov.Kind()))

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnparseable
	}
	return []byte(text), nil
}
