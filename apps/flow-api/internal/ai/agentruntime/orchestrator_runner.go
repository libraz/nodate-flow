package agentruntime

import (
	"context"
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
)

// AgentExecutor is the narrow dependency that OrchestratorRunner needs
// from the ai package. Production wiring passes an adapter around
// [ai.Orchestrator] that loads the agent row, resolves the workspace
// provider, and calls Complete with the agent's system prompt. Tests
// pass a stub that records calls without touching a real LLM client.
//
// Splitting the interface here (instead of taking *ai.Orchestrator
// directly) keeps agentruntime free of the ai import cycle: the ai
// package already depends on agentruntime's sibling packages, so
// inverting the dependency at this boundary is what lets main.go wire
// everything together without a helper package.
type AgentExecutor interface {
	// ExecuteAgent runs one tick of the agent identified by agentID in
	// the given workspace. Implementations resolve the agent's model +
	// system prompt, invoke the provider, and return any error so the
	// runner can Nack the queue row and append an ai.agent.run.failed
	// event.
	ExecuteAgent(ctx context.Context, workspaceID, agentID uint32) error
}

// OrchestratorRunner is a [Runner] that delegates the LLM call to an
// [AgentExecutor] and writes ai.agent.run.* events around it. It
// replaces [LogRunner] once the api is wired to a real orchestrator;
// the LogRunner stays as the single-binary scaffold default so tests
// and smoke runs do not need a provider configured.
type OrchestratorRunner struct {
	DB       *sql.DB
	Executor AgentExecutor
	// Now is injected so tests can assert deterministic event
	// timestamps; defaults to time.Now when nil.
	Now func() time.Time
}

// Run implements [Runner]. It appends ai.agent.run.started before the
// call and ai.agent.run.completed / ai.agent.run.failed after. The
// events are best-effort: a failing eventbus.Append is swallowed so a
// flaky events table cannot block the agent loop.
func (r *OrchestratorRunner) Run(ctx context.Context, j Job, _ time.Time) error {
	if r.Now == nil {
		r.Now = time.Now
	}
	started := r.Now().UTC()
	_ = eventbus.Append(ctx, r.DB, eventbus.Event{
		Type:        eventbus.AiAgentRunStarted,
		WorkspaceID: j.WsID,
		Payload: map[string]any{
			"agentId":   j.AgentID,
			"startedAt": started.Unix(),
		},
	})
	var runErr error
	if r.Executor != nil {
		runErr = r.Executor.ExecuteAgent(ctx, j.WsID, j.AgentID)
	}
	finished := r.Now().UTC()
	if runErr != nil {
		_ = eventbus.Append(ctx, r.DB, eventbus.Event{
			Type:        eventbus.AiAgentRunFailed,
			WorkspaceID: j.WsID,
			Payload: map[string]any{
				"agentId":    j.AgentID,
				"startedAt":  started.Unix(),
				"finishedAt": finished.Unix(),
				"error":      runErr.Error(),
			},
		})
		return runErr
	}
	_ = eventbus.Append(ctx, r.DB, eventbus.Event{
		Type:        eventbus.AiAgentRunCompleted,
		WorkspaceID: j.WsID,
		Payload: map[string]any{
			"agentId":    j.AgentID,
			"startedAt":  started.Unix(),
			"finishedAt": finished.Unix(),
		},
	})
	return nil
}
