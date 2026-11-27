package agentruntime

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Trigger describes the event that woke an on_event run. The zero value
// means nothing triggered the run: an interval tick or a manual run.
type Trigger struct {
	// EventType is the eventbus kind of the source event.
	EventType string
	// TaskPublicID is the task the source event is bound to, empty for a
	// workspace-level event. The task's content is deliberately not
	// carried: the prompt is logged to ai_invocations, which every
	// workspace member can read, so a private task's title would leak.
	TaskPublicID string
	// TaskID is the internal id of the same task, zero when there is none.
	TaskID uint32
	// SourceEventID is the events.id of the source event.
	SourceEventID int64
	// TriggerUserID is the source event's actor_user_id, zero when absent.
	TriggerUserID uint32
	// TaskWork is true when the source event starts a task-work run rather
	// than a tick; see isTaskWorkSource.
	TaskWork bool
}

type triggerCtxKey struct{}

// WithTrigger returns a copy of ctx carrying t. [OrchestratorRunner.Run]
// sets it before dispatching so an [AgentExecutor] can build its prompt
// from the source event without widening the executor interface.
func WithTrigger(ctx context.Context, t Trigger) context.Context {
	if t == (Trigger{}) {
		return ctx
	}
	return context.WithValue(ctx, triggerCtxKey{}, t)
}

// TriggerFromContext returns the Trigger set via WithTrigger, or the
// zero value when the run was not event-triggered.
func TriggerFromContext(ctx context.Context) Trigger {
	if ctx == nil {
		return Trigger{}
	}
	t, _ := ctx.Value(triggerCtxKey{}).(Trigger)
	return t
}

type runKeyCtxKey struct{}

// WithRunKey returns a copy of ctx carrying the run's key, the UUID that
// ties a run's events and steps together.
func WithRunKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, runKeyCtxKey{}, key)
}

// RunKeyFromContext returns the key set via WithRunKey, or "".
func RunKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	k, _ := ctx.Value(runKeyCtxKey{}).(string)
	return k
}

// Run modes written to the ai.agent.run.* payloads.
const (
	RunModeTaskWork = "task_work"
	RunModeTick     = "tick"
)

// sourceEvent is the part of a source event that decides the run mode.
type sourceEvent struct {
	Type           string
	TaskID         uint32
	ActorUserID    uint32
	ActorAgentID   uint32
	PayloadAgentID string
	PayloadRole    string
}

// isTaskWorkSource reports whether ev starts task work for the agent: a
// human-caused event on a task that is an assignment of this agent, a
// handoff or run request addressed to it, or a comment. The agent, its
// assignment and the task are rechecked by the executor; this mirrors the
// event half of the candidate query in event_source.go.
func isTaskWorkSource(ev sourceEvent, agentPublicID string) bool {
	if ev.TaskID == 0 || ev.ActorUserID == 0 || ev.ActorAgentID != 0 {
		return false
	}
	switch eventbus.Kind(ev.Type) {
	case eventbus.TaskActorAdded:
		return sameAgent(ev.PayloadAgentID, agentPublicID) && ev.PayloadRole == string(generated.TaskActorsRoleAssignee)
	case eventbus.AgentTaskHandoffToAgent, eventbus.AgentTaskRunRequested:
		return sameAgent(ev.PayloadAgentID, agentPublicID)
	case eventbus.TaskCommentAdded:
		return true
	}
	return false
}

func sameAgent(payloadAgentID, agentPublicID string) bool {
	a, err := types.Parse(payloadAgentID)
	if err != nil {
		return false
	}
	b, err := types.Parse(agentPublicID)
	return err == nil && a == b
}
