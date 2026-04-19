// Package ai — agent attribution context key.
//
// When an MCP tool call is made on behalf of an AI agent,
// the dispatch layer wraps the request context with WithAgentID so
// downstream orchestrator calls can attribute their ai_invocations
// rows to that agent. When the context carries no agent id (human
// caller) AgentIDFromContext returns zero.
package ai

import "context"

type agentCtxKey struct{}

// WithAgentID returns a copy of ctx tagged with agentID. Callers in
// the MCP dispatch layer set this before invoking orchestrator
// methods. Passing zero is a no-op.
func WithAgentID(ctx context.Context, agentID uint32) context.Context {
	if agentID == 0 {
		return ctx
	}
	return context.WithValue(ctx, agentCtxKey{}, agentID)
}

// AgentIDFromContext returns the agent id previously set via
// WithAgentID, or zero when the context was never tagged.
func AgentIDFromContext(ctx context.Context) uint32 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(agentCtxKey{}).(uint32)
	return v
}
