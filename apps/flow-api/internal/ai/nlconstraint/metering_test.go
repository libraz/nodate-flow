// Guard + metering tests for the workspace-backed nlconstraint provider.
//
// These lock in audit fix C-2 (H-8 / M-6): the NL-constraint compiler
// surface must enforce the per-workspace daily budget before billing the
// provider and must write a redacted ai_invocations row (with cost) for
// every successful call, exactly like the orchestrator path.
package nlconstraint

import (
	"context"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// meteringResolver returns a fixed provider on every call.
type meteringResolver struct {
	provider providers.Provider
	err      error
}

func (r *meteringResolver) Default(_ context.Context, _ uint32) (providers.Provider, error) {
	return r.provider, r.err
}

// recordingProvider captures whether it was called and returns a canned
// response or error. Implements providers.Provider.
type recordingProvider struct {
	kind   providers.Kind
	resp   *providers.Response
	err    error
	called bool
}

func (p *recordingProvider) Name() string         { return "recording" }
func (p *recordingProvider) Kind() providers.Kind { return p.kind }

func (p *recordingProvider) Complete(_ context.Context, _ providers.Request) (*providers.Response, error) {
	p.called = true
	if p.err != nil {
		return nil, p.err
	}
	return p.resp, nil
}

// fixedGuard returns a fixed error, standing in for ai.CostGuard.
type fixedGuard struct{ err error }

func (g fixedGuard) Check(_ context.Context, _ uint32) error { return g.err }

func extractWSFixed(id uint32) WorkspaceIDFromContext {
	return func(context.Context) (uint32, bool) { return id, true }
}

const goodConstraint = `{"op":"time.due_before","arg":"2026-01-01"}`

// TestCompileConstraint_BudgetExhaustedBlocks proves the workspace daily
// budget guard short-circuits the call: when the guard reports the cap is
// hit, CompileConstraint returns ErrBudgetExceeded and never bills the
// provider.
func TestCompileConstraint_BudgetExhaustedBlocks(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{kind: providers.Kind("mock"), resp: &providers.Response{Text: goodConstraint}}
	wp := NewWorkspaceProvider(&meteringResolver{provider: prov}, extractWSFixed(7)).
		WithMetering(fixedGuard{err: ai.ErrDailyBudgetExceeded}, nil, nil)

	_, err := wp.CompileConstraint(context.Background(), "due before new year")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("CompileConstraint error = %v, want ErrBudgetExceeded", err)
	}
	if prov.called {
		t.Fatal("provider was billed despite exhausted budget")
	}
}

// TestCompileConstraint_SuccessLogsInvocationWithCost proves a successful
// NL call routes through the same metering path as the orchestrator: it
// logs one ai_invocations record carrying the redacted prompt/response,
// token counts, and cost cents.
func TestCompileConstraint_SuccessLogsInvocationWithCost(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		resp: &providers.Response{Text: goodConstraint, InputTokens: 31, OutputTokens: 12, CostCents: 42},
	}
	var logged []InvocationRecord
	wp := NewWorkspaceProvider(&meteringResolver{provider: prov}, extractWSFixed(7)).
		WithMetering(fixedGuard{}, func(_ context.Context, rec InvocationRecord) {
			logged = append(logged, rec)
		}, nil)

	out, err := wp.CompileConstraint(context.Background(), "due before new year")
	if err != nil {
		t.Fatalf("CompileConstraint: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty compiled bytes")
	}
	if len(logged) != 1 {
		t.Fatalf("logged %d invocations, want exactly 1", len(logged))
	}
	rec := logged[0]
	if rec.WorkspaceID != 7 {
		t.Errorf("WorkspaceID = %d, want 7", rec.WorkspaceID)
	}
	if rec.Status != "ok" {
		t.Errorf("Status = %q, want ok", rec.Status)
	}
	if rec.CostCents != 42 {
		t.Errorf("CostCents = %d, want 42", rec.CostCents)
	}
	if rec.TokensInput != 31 || rec.TokensOutput != 12 {
		t.Errorf("tokens = (%d,%d), want (31,12)", rec.TokensInput, rec.TokensOutput)
	}
	if rec.PromptRedacted == "" {
		t.Error("PromptRedacted empty, want redacted prompt")
	}
	if rec.ResponseRedacted == "" {
		t.Error("ResponseRedacted empty, want redacted response")
	}
}

// TestCompileConstraint_ProviderErrorLogsFailure proves a provider error
// still produces an ai_invocations row so failed spend / errors are
// attributable.
func TestCompileConstraint_ProviderErrorLogsFailure(t *testing.T) {
	t.Parallel()
	prov := &recordingProvider{kind: providers.Kind("mock"), err: errors.New("boom")}
	var logged []InvocationRecord
	wp := NewWorkspaceProvider(&meteringResolver{provider: prov}, extractWSFixed(7)).
		WithMetering(fixedGuard{}, func(_ context.Context, rec InvocationRecord) {
			logged = append(logged, rec)
		}, nil)

	if _, err := wp.CompileConstraint(context.Background(), "due before new year"); err == nil {
		t.Fatal("expected provider error")
	}
	if len(logged) != 1 || logged[0].Status != "error" {
		t.Fatalf("want one error invocation, got %+v", logged)
	}
}
