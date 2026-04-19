package ai

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// ErrAgentPaused is returned by [AgentExecutor.ExecuteAgent] when the
// target agent row has paused=TRUE. The runner turns this into an
// ai.agent.run.failed event with a known error code so the UI can
// distinguish "paused" from an LLM failure.
var ErrAgentPaused = errors.New("ai: agent is paused")

// AgentExecutor runs one tick of an AI agent. It is the production
// adapter behind the [agentruntime.AgentExecutor] interface — the
// runner stays in the agentruntime package and never imports the ai
// package directly, so this type lives here and is injected from
// main.go after both sides are constructed.
//
// Execute loads the agent row via [generated.Queries.GetAgentForExec],
// enforces the cost guard, resolves the workspace's default provider,
// and calls Complete with the agent's system prompt. The redacted
// prompt + response are persisted via [InvocationLogger] alongside
// every other LLM call site.
type AgentExecutor struct {
	Queries      *generated.Queries
	Resolver     ProviderResolver
	Guard        *CostGuard
	Log          InvocationLogger
	OnInvocation InvocationMetricsHook
	// PreFlight is an optional check that skips the LLM call when
	// no new events have occurred since the agent's last successful
	// run. When nil the check is bypassed and every tick invokes the
	// provider.
	PreFlight *PreFlight
}

// ExecuteAgent implements the agentruntime.AgentExecutor contract.
// The agentID argument is the internal ai_agents.id because the
// runner already has it in hand from DBSource; looking it up again
// by public id would cost an extra round-trip per tick.
func (e *AgentExecutor) ExecuteAgent(ctx context.Context, workspaceID, agentID uint32) error {
	if e == nil || e.Queries == nil || e.Resolver == nil {
		return ErrNoProvider
	}
	row, err := e.Queries.GetAgentForExec(ctx, generated.GetAgentForExecParams{
		WorkspaceID: workspaceID,
		ID:          agentID,
	})
	if err != nil {
		return fmt.Errorf("ai: agent lookup failed: %w", err)
	}
	if row.Paused {
		return ErrAgentPaused
	}
	// Pre-flight: skip LLM call if no new events since last successful run.
	if e.PreFlight != nil {
		if skip, _ := e.PreFlight.ShouldSkip(ctx, workspaceID, agentID); skip {
			return nil
		}
	}
	if e.Guard != nil {
		if err := e.Guard.Check(ctx, workspaceID); err != nil {
			return err
		}
	}
	prov, err := e.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return err
	}
	if prov == nil {
		return ErrNoProvider
	}
	ctx = providers.WithWorkspaceID(ctx, workspaceID)
	req := providers.Request{
		System: row.SystemPrompt,
		// The tick prompt is intentionally empty — interval agents
		// are self-directed; the system prompt defines the task and
		// the model replies without additional user input. Event /
		// manual triggers will populate Prompt in a later pass.
		Prompt: "",
	}
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		if e.OnInvocation != nil {
			e.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, 0)
		}
		if o := (&Orchestrator{LogInvoke: e.Log}); o.LogInvoke != nil {
			o.logFailure(ctx, workspaceID, "agent_tick", req, err)
		}
		return fmt.Errorf("ai: agent provider call failed: %w", err)
	}
	if e.OnInvocation != nil {
		e.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, resp.CostCents)
	}
	if o := (&Orchestrator{LogInvoke: e.Log}); o.LogInvoke != nil {
		o.logSuccess(ctx, workspaceID, "agent_tick", req, resp)
	}
	return nil
}
