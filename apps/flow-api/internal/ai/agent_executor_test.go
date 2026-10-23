package ai

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// stubExecQueries returns a fixed agent row, standing in for the sqlc
// handle so a tick can run without a database. capCents (when non-nil)
// and spentCents feed the monthly cost cap check.
type stubExecQueries struct {
	row        generated.GetAgentForExecRow
	err        error
	capCents   *int32
	spentCents int64
}

func (s stubExecQueries) GetAgentForExec(_ context.Context, _ generated.GetAgentForExecParams) (generated.GetAgentForExecRow, error) {
	return s.row, s.err
}

func (s stubExecQueries) GetAgentGuardSnapshot(_ context.Context, _ uint32) (generated.GetAgentGuardSnapshotRow, error) {
	row := generated.GetAgentGuardSnapshotRow{Enabled: true}
	if s.capCents != nil {
		row.MonthlyCostCapCents = sql.NullInt32{Int32: *s.capCents, Valid: true}
	}
	return row, nil
}

func (s stubExecQueries) SumAiCostForAgentSince(_ context.Context, _ generated.SumAiCostForAgentSinceParams) (int64, error) {
	return s.spentCents, nil
}

// stubProvider is a Provider that answers without any network access. A
// non-nil failWith makes Complete fail so the error-logging path can be
// exercised too.
type stubProvider struct {
	failWith error
}

func (stubProvider) Name() string         { return "stub" }
func (stubProvider) Kind() providers.Kind { return providers.KindOllama }
func (stubProvider) Model() string        { return "stub-default" }
func (p stubProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	if p.failWith != nil {
		return nil, p.failWith
	}
	return &providers.Response{Model: req.Model, Text: "done", InputTokens: 3, OutputTokens: 4, CostMicros: 5_000}, nil
}

// newTestExecutor builds an executor whose invocation logger appends into
// recorded, wired to a provider that either answers or fails.
func newTestExecutor(recorded *[]InvocationRecord, prov providers.Provider) *AgentExecutor {
	return &AgentExecutor{
		Queries: stubExecQueries{row: generated.GetAgentForExecRow{
			ID:              7,
			WorkspaceID:     42,
			Name:            "nightly",
			SystemPrompt:    "be useful",
			Temperature:     70,
			MaxOutputTokens: sql.NullInt32{Int32: 256, Valid: true},
			ModelName:       "stub-model",
		}},
		Resolver: AgentProviderResolverFunc(func(context.Context, uint32, types.PublicID) (providers.Provider, error) {
			return prov, nil
		}),
		Log: func(_ context.Context, rec InvocationRecord) {
			*recorded = append(*recorded, rec)
		},
	}
}

// TestExecuteAgentAttributesInvocationToAgent pins the attribution a
// scheduled agent run must carry. ai_invocations.agent_id is written from
// the context tag, and the per-agent monthly cost cap is summed over rows
// that have it; a run logged with agent_id NULL is spend the cap can never
// see. Nothing above ExecuteAgent tags the context for interval runs, so
// the tag has to be applied here.
func TestExecuteAgentAttributesInvocationToAgent(t *testing.T) {
	t.Parallel()

	const (
		workspaceID uint32 = 42
		agentID     uint32 = 7
	)

	t.Run("successful tick", func(t *testing.T) {
		t.Parallel()

		var recorded []InvocationRecord
		e := newTestExecutor(&recorded, stubProvider{})

		res, err := e.ExecuteAgent(context.Background(), workspaceID, agentID)
		if err != nil {
			t.Fatalf("ExecuteAgent: %v", err)
		}
		if res.CostMicros != 5_000 {
			t.Fatalf("CostMicros = %d, want 5000", res.CostMicros)
		}
		if len(recorded) != 1 {
			t.Fatalf("logged %d invocations, want 1", len(recorded))
		}
		if recorded[0].AgentID != agentID {
			t.Fatalf("logged AgentID = %d, want %d; an untagged run is invisible to the per-agent monthly cap", recorded[0].AgentID, agentID)
		}
		if recorded[0].Status != "ok" {
			t.Fatalf("logged Status = %q, want ok", recorded[0].Status)
		}
	})

	t.Run("failed tick", func(t *testing.T) {
		t.Parallel()

		var recorded []InvocationRecord
		e := newTestExecutor(&recorded, stubProvider{failWith: errors.New("upstream is down")})

		if _, err := e.ExecuteAgent(context.Background(), workspaceID, agentID); err == nil {
			t.Fatal("ExecuteAgent succeeded, want the provider error")
		}
		if len(recorded) != 1 {
			t.Fatalf("logged %d invocations, want 1", len(recorded))
		}
		if recorded[0].AgentID != agentID {
			t.Fatalf("logged AgentID = %d, want %d", recorded[0].AgentID, agentID)
		}
		if recorded[0].Status != "error" {
			t.Fatalf("logged Status = %q, want error", recorded[0].Status)
		}
	})
}

// TestExecuteAgentTaggedContextReachesProvider asserts the agent tag is on
// the context the provider itself is called with, not only on the one the
// logger happens to see. Anything the provider call fans out to — a nested
// orchestrator call, a metrics hook that reads the tag — inherits it.
func TestExecuteAgentTaggedContextReachesProvider(t *testing.T) {
	t.Parallel()

	var seen uint32
	var recorded []InvocationRecord
	e := newTestExecutor(&recorded, providerFunc(func(ctx context.Context, req providers.Request) (*providers.Response, error) {
		seen = AgentIDFromContext(ctx)
		return &providers.Response{Model: req.Model, Text: "ok"}, nil
	}))

	if _, err := e.ExecuteAgent(context.Background(), 42, 7); err != nil {
		t.Fatalf("ExecuteAgent: %v", err)
	}
	if seen != 7 {
		t.Fatalf("agent id on the provider's context = %d, want 7", seen)
	}
}

// providerFunc adapts a plain completion function to providers.Provider.
type providerFunc func(context.Context, providers.Request) (*providers.Response, error)

func (providerFunc) Name() string         { return "stub" }
func (providerFunc) Kind() providers.Kind { return providers.KindOllama }
func (providerFunc) Model() string        { return "stub-default" }
func (f providerFunc) Complete(ctx context.Context, req providers.Request) (*providers.Response, error) {
	return f(ctx, req)
}

// TestExecuteAgentUsesTheProviderOwningTheAgentsModel pins that the agent's
// model is sent to the provider row that owns it, not to the workspace
// default: a model name from one provider kind is meaningless, and its key
// and endpoint are different, on another.
func TestExecuteAgentUsesTheProviderOwningTheAgentsModel(t *testing.T) {
	t.Parallel()

	owner := types.New()
	var got types.PublicID
	var recorded []InvocationRecord
	exec := newTestExecutor(&recorded, &stubProvider{})
	exec.Queries = stubExecQueries{row: generated.GetAgentForExecRow{
		ID:               7,
		WorkspaceID:      42,
		ModelName:        "stub-model",
		ProviderPublicID: owner,
	}}
	exec.Resolver = AgentProviderResolverFunc(func(_ context.Context, _ uint32, id types.PublicID) (providers.Provider, error) {
		got = id
		return &stubProvider{}, nil
	})

	if _, err := exec.ExecuteAgent(context.Background(), 42, 7); err != nil {
		t.Fatalf("ExecuteAgent: %v", err)
	}
	if got != owner {
		t.Fatalf("resolved provider = %v, want the model's owning provider %v", got, owner)
	}
}

// TestExecuteAgentRefusesAnAgentAtItsMonthlyCap pins that a scheduled run
// is refused before any provider call once the agent's month-to-date spend
// reaches its monthly cap, and that the refusal is flagged as a cost-cap
// hit so the runner pauses the agent and hands the task off.
func TestExecuteAgentRefusesAnAgentAtItsMonthlyCap(t *testing.T) {
	t.Parallel()

	capCents := int32(1_000)
	called := false
	var recorded []InvocationRecord
	e := newTestExecutor(&recorded, providerFunc(func(_ context.Context, req providers.Request) (*providers.Response, error) {
		called = true
		return &providers.Response{Model: req.Model, Text: "ok"}, nil
	}))
	q := e.Queries.(stubExecQueries)
	q.capCents = &capCents
	q.spentCents = 1_000
	e.Queries = q

	res, err := e.ExecuteAgent(context.Background(), 42, 7)
	if !errors.Is(err, ErrAgentCostCapExceeded) {
		t.Fatalf("ExecuteAgent err = %v, want ErrAgentCostCapExceeded", err)
	}
	if !res.CostCapHit {
		t.Fatal("CostCapHit = false, want true so the runner pauses the agent")
	}
	if called {
		t.Fatal("provider was called for an agent at its monthly cap")
	}

	q.spentCents = 100
	e.Queries = q
	if _, err := e.ExecuteAgent(context.Background(), 42, 7); err != nil {
		t.Fatalf("ExecuteAgent under the cap: %v", err)
	}
	if !called {
		t.Fatal("provider was not called for an agent under its monthly cap")
	}
}

// TestExecuteAgentSendsANonEmptyUserTurn pins the user turn of each run
// kind: providers reject an empty one, and an on_event run must tell the
// model which event and task woke it.
func TestExecuteAgentSendsANonEmptyUserTurn(t *testing.T) {
	t.Parallel()

	run := func(ctx context.Context) string {
		var prompt string
		var recorded []InvocationRecord
		e := newTestExecutor(&recorded, providerFunc(func(_ context.Context, req providers.Request) (*providers.Response, error) {
			prompt = req.Prompt
			return &providers.Response{Model: req.Model, Text: "ok"}, nil
		}))
		if _, err := e.ExecuteAgent(ctx, 42, 7); err != nil {
			t.Fatalf("ExecuteAgent: %v", err)
		}
		return prompt
	}

	if got := run(context.Background()); got == "" {
		t.Fatal("interval run sent an empty user turn")
	}
	got := run(agentruntime.WithTrigger(context.Background(), agentruntime.Trigger{
		EventType:    "task.transition.complete",
		TaskPublicID: "0192f0c4-0000-7000-8000-000000000001",
	}))
	for _, want := range []string{"task.transition.complete", "0192f0c4-0000-7000-8000-000000000001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("on_event user turn %q does not contain %q", got, want)
		}
	}
}
