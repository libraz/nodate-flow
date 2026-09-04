package ai

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// ErrAgentPaused is returned by [AgentExecutor.ExecuteAgent] when the
// target agent row has paused=TRUE. The runner turns this into an
// ai.agent.run.failed event with a known error code so the UI can
// distinguish "paused" from an LLM failure.
var ErrAgentPaused = errors.New("ai: agent is paused")

// AgentExecQueries is the slice of the sqlc Querier that ExecuteAgent
// needs. *generated.Queries satisfies it; it is an interface rather than
// the concrete handle so the attribution and cost-accounting behaviour of
// one tick can be exercised without a database.
type AgentExecQueries interface {
	GetAgentForExec(ctx context.Context, arg generated.GetAgentForExecParams) (generated.GetAgentForExecRow, error)
}

// AgentExecutor runs one tick of an AI agent. It is the production
// adapter behind the [agentruntime.AgentExecutor] interface — the
// runner stays in the agentruntime package and never imports the ai
// package directly, so this type lives here and is injected from
// main.go after both sides are constructed.
//
// Execute loads the agent row via [generated.Queries.GetAgentForExec],
// enforces the cost guard, resolves the workspace's default provider,
// and calls Complete with the agent's system prompt, model, output cap,
// and temperature — all of which come from that row via
// [airequest.ForAgent], not from the provider's own defaults. The
// redacted prompt + response are persisted via [InvocationLogger]
// alongside every other LLM call site, tagged with the agent that caused
// them so per-agent cost accounting can see them.
type AgentExecutor struct {
	Queries      AgentExecQueries
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
func (e *AgentExecutor) ExecuteAgent(ctx context.Context, workspaceID, agentID uint32) (agentruntime.ExecutionResult, error) {
	var result agentruntime.ExecutionResult
	if e == nil || e.Queries == nil || e.Resolver == nil {
		return result, ErrNoProvider
	}
	// Attribute everything this tick spends to the agent that caused it.
	// The per-agent monthly cap is enforced from SumAiCostForAgentSince,
	// which only counts ai_invocations rows whose agent_id is set, and the
	// only writer of that column is AgentIDFromContext at the logging call
	// sites. An MCP tool call gets tagged by the dispatch layer above it; a
	// scheduled run has no such layer, so without this the cap is enforced
	// against a spend total that is permanently zero. The judge runner
	// passes the same id explicitly into its own InvocationRecord.
	ctx = WithAgentID(ctx, agentID)
	row, err := e.Queries.GetAgentForExec(ctx, generated.GetAgentForExecParams{
		WorkspaceID: workspaceID,
		ID:          agentID,
	})
	if err != nil {
		return result, fmt.Errorf("ai: agent lookup failed: %w", err)
	}
	if row.Paused {
		return result, ErrAgentPaused
	}
	// Pre-flight: skip LLM call if no new events since last successful run.
	if e.PreFlight != nil {
		if skip, _ := e.PreFlight.ShouldSkip(ctx, workspaceID, agentID); skip {
			return result, nil
		}
	}
	if e.Guard != nil {
		if err := e.Guard.Check(ctx, workspaceID); err != nil {
			// The runner uses CostCapHit to fire the cost_cap handoff
			// trigger and pause the agent, so distinguish budget hits
			// from generic guard read failures.
			if errors.Is(err, ErrDailyBudgetExceeded) {
				result.CostCapHit = true
			}
			return result, err
		}
	}
	prov, err := e.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	if prov == nil {
		return result, ErrNoProvider
	}
	ctx = providers.WithWorkspaceID(ctx, workspaceID)
	req := airequest.ForAgent(prov, airequest.FromExecRow(row), airequest.Args{
		System: row.SystemPrompt,
		// The tick prompt is intentionally empty — interval agents
		// are self-directed; the system prompt defines the task and
		// the model replies without additional user input. Event /
		// manual triggers will populate Prompt in a later pass.
		Prompt: "",
	})
	wsIDStr := strconv.FormatUint(uint64(workspaceID), 10)
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		if e.OnInvocation != nil {
			e.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, 0, err)
		}
		if o := (&Orchestrator{LogInvoke: e.Log}); o.LogInvoke != nil {
			o.logFailure(ctx, workspaceID, "agent_tick", req, err)
		}
		return result, fmt.Errorf("ai: agent provider call failed: %w", err)
	}
	if e.OnInvocation != nil {
		e.OnInvocation(string(prov.Kind()), req.Model, wsIDStr, resp.EstimatedCostMicros(), nil)
	}
	if o := (&Orchestrator{LogInvoke: e.Log}); o.LogInvoke != nil {
		o.logSuccess(ctx, workspaceID, "agent_tick", req, resp)
	}
	result.CostMicros = resp.EstimatedCostMicros()
	result.CostCents = resp.EstimatedCostCents()
	// The raw LLM response is persisted to tasks.agent_memo.last_thought
	// and served back through the API lastThought field; redact any
	// secret-prefixed token the model may have echoed before it leaves
	// this call and reaches storage.
	result.LastThought = logutil.Redact(resp.Text)
	return result, nil
}
