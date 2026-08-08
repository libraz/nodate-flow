package ai

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// stubExecQueries returns a fixed agent row, standing in for the sqlc
// handle so a tick can run without a database.
type stubExecQueries struct {
	row generated.GetAgentForExecRow
	err error
}

func (s stubExecQueries) GetAgentForExec(_ context.Context, _ generated.GetAgentForExecParams) (generated.GetAgentForExecRow, error) {
	return s.row, s.err
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
		Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
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
