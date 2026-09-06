package nlcommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// ErrBudgetExceeded is returned by ResolveCommand when the workspace has
// already spent more than its daily LLM budget. The HTTP layer maps it to
// AI.COST_GUARD.EXCEEDED, mirroring the orchestrator's
// ai.ErrDailyBudgetExceeded handling so every NL surface trips the same
// per-workspace cap.
var ErrBudgetExceeded = errors.New("nlcommand: daily budget exceeded")

// CostGuard is the narrow contract WorkspaceProvider needs to participate
// in the per-workspace daily cap. It mirrors ai.CostGuard.Check so the
// command-palette path shares one budget with the orchestrator and
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
	CostMicros       int64
	CostCents        int64
	Status           string
	ErrorCode        string
}

// InvocationMetricsHook is called after each LLM provider call. Same
// shape as ai.InvocationMetricsHook so obs.RecordAIInvocation is reused
// without an adapter. elapsed measures the provider call alone; the token
// counts come from the response and are 0 on a failure. err is nil on a
// successful call and carries the provider's error on a failed one.
type InvocationMetricsHook func(provider, model string, inputTokens, outputTokens int, costMicros int64, elapsed time.Duration, err error)

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
- You are not given any public ids. When a schema asks for one (taskId,
  projectId, ...) and the user named the thing by its title instead, put
  that title text in the id field verbatim; the caller looks it up. Never
  invent a UUID.
- Moving a task between states is "transition_task", not a title or field
  update.

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

	// Guard enforces the per-workspace daily spend cap before the LLM
	// call. Nil disables enforcement (MVP / mock paths).
	Guard CostGuard
	// LogInvoke persists a redacted ai_invocations row after the call so
	// command-palette spend is tracked, attributable, and counted against
	// the daily budget. Nil disables logging.
	LogInvoke InvocationLogger
	// OnInvocation records the Prometheus cost metric. Nil disables it.
	OnInvocation InvocationMetricsHook
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

// WithMetering wires the cost guard, invocation logger, and metrics hook
// so the command-palette path enforces the daily budget and records
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
//
// promptRedacted is stored as it arrives. ai_invocations is served to
// every member of the workspace, so the combined system + user prompt is
// scrubbed by logutil.Redact where it is built, once: redacting again
// here would nest a marker inside the replacement text of the first pass
// ("sk-" inside "[REDACTED:sk-]" matches the scanner again).
func (w *WorkspaceProvider) logSuccess(ctx context.Context, wsID uint32, model, promptRedacted string, resp *providers.Response, kind string, elapsed time.Duration) {
	if w.OnInvocation != nil {
		w.OnInvocation(kind, model, resp.InputTokens, resp.OutputTokens, resp.EstimatedCostMicros(), elapsed, nil)
	}
	if w.LogInvoke != nil {
		loggedModel := resp.Model
		if loggedModel == "" {
			loggedModel = model
		}
		w.LogInvoke(ctx, InvocationRecord{
			WorkspaceID:      wsID,
			Purpose:          "resolve_command",
			Model:            loggedModel,
			PromptRedacted:   promptRedacted,
			ResponseRedacted: logutil.Redact(resp.Text),
			TokensInput:      resp.InputTokens,
			TokensOutput:     resp.OutputTokens,
			CostMicros:       resp.EstimatedCostMicros(),
			CostCents:        resp.EstimatedCostCents(),
			Status:           "ok",
		})
	}
}

// logFailure records the redacted invocation + zero-cost metric after a
// failed provider call. This row has the same workspace-wide audience as
// the successful one, so promptRedacted arrives already scrubbed here
// too; see [WorkspaceProvider.logSuccess].
func (w *WorkspaceProvider) logFailure(ctx context.Context, wsID uint32, model, promptRedacted string, callErr error, kind string, elapsed time.Duration) {
	if w.OnInvocation != nil {
		w.OnInvocation(kind, model, 0, 0, 0, elapsed, callErr)
	}
	if w.LogInvoke != nil {
		w.LogInvoke(ctx, InvocationRecord{
			WorkspaceID:    wsID,
			Purpose:        "resolve_command",
			Model:          model,
			PromptRedacted: promptRedacted,
			Status:         "error",
			ErrorCode:      logutil.Redact(callErr.Error()),
		})
	}
}

// ResolveCommand implements Provider. It resolves the workspace's
// default LLM provider, builds a system prompt that includes the tool
// catalogue, and returns the raw response bytes.
func (w *WorkspaceProvider) ResolveCommand(ctx context.Context, prompt string, tools []ToolSpec) ([]byte, error) {
	wsID, ok := w.ExtractWS(ctx)
	if !ok {
		return nil, fmt.Errorf("nlcommand: workspace ID not found in context")
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
			return nil, fmt.Errorf("nlcommand: cost guard: %w", err)
		}
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

	req := airequest.New(prov, airequest.Args{
		System:    system,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
	promptRedacted := logutil.Redact(strings.TrimSpace(req.System + "\n" + req.Prompt))
	// time.Since is taken inside each branch rather than bound to a local
	// here: the latency this reports must cover the provider call and
	// nothing else, and the error check has to stay adjacent to the call
	// it checks.
	start := time.Now()
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		w.logFailure(ctx, wsID, req.Model, promptRedacted, err, string(prov.Kind()), time.Since(start))
		return nil, fmt.Errorf("nlcommand: provider call failed: %w", err)
	}
	w.logSuccess(ctx, wsID, req.Model, promptRedacted, resp, string(prov.Kind()), time.Since(start))

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, ErrUnresolvable
	}
	return []byte(text), nil
}
