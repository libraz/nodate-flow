package nlquery

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

// ErrBudgetExceeded is returned by CompileLens when the workspace has
// already spent more than its daily LLM budget. The HTTP layer maps it to
// AI.COST_GUARD.EXCEEDED, mirroring the orchestrator's
// ai.ErrDailyBudgetExceeded handling so every NL surface trips the same
// per-workspace cap.
var ErrBudgetExceeded = errors.New("nlquery: daily budget exceeded")

// CostGuard is the narrow contract WorkspaceProvider needs to participate
// in the per-workspace daily cap. It mirrors ai.CostGuard.Check so the
// NL-query path shares one budget with the orchestrator and signal-judge
// (ADR 0008 D3). A nil guard disables enforcement.
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

	// Guard enforces the per-workspace daily spend cap before the LLM
	// call. Nil disables enforcement (MVP / mock paths).
	Guard CostGuard
	// LogInvoke persists a redacted ai_invocations row after the call so
	// NL-query spend is tracked, attributable, and counted against the
	// daily budget. Nil disables logging.
	LogInvoke InvocationLogger
	// OnInvocation records the Prometheus cost metric. Nil disables it.
	OnInvocation InvocationMetricsHook
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

// WithMetering wires the cost guard, invocation logger, and metrics hook
// so the NL-query path enforces the daily budget and records redacted
// ai_invocations rows like the orchestrator. Returns the receiver for
// fluent construction.
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
			Purpose:          "compile_lens",
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
			Purpose:        "compile_lens",
			Model:          model,
			PromptRedacted: prompt,
			Status:         "error",
			ErrorCode:      logutil.Redact(callErr.Error()),
		})
	}
}

// CompileLens implements Provider. It resolves the workspace's default LLM
// provider from the request context, sends the system prompt + user prose,
// and returns the raw response bytes.
func (w *WorkspaceProvider) CompileLens(ctx context.Context, prompt string) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlquery: workspace ID not found in context")
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
			return nil, fmt.Errorf("nlquery: cost guard: %w", err)
		}
	}

	prov, err := w.Resolver.Default(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("nlquery: resolve provider: %w", err)
	}
	if prov == nil {
		return nil, fmt.Errorf("nlquery: no provider configured for workspace")
	}
	ctx = providers.WithWorkspaceID(ctx, wsID)

	req := providers.Request{
		System:    compileLensSystem,
		Prompt:    prompt,
		MaxTokens: 1024,
	}
	wsIDStr := strconv.FormatUint(uint64(wsID), 10)
	promptRedacted := logutil.Redact(strings.TrimSpace(req.System + "\n" + req.Prompt))
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		w.logFailure(ctx, wsID, req.Model, wsIDStr, promptRedacted, err, string(prov.Kind()))
		return nil, fmt.Errorf("nlquery: provider call failed: %w", err)
	}
	w.logSuccess(ctx, wsID, req.Model, wsIDStr, promptRedacted, resp, string(prov.Kind()))

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnparseable
	}
	return []byte(text), nil
}
