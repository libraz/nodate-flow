package agentruntime

import "context"

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
