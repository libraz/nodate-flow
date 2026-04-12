// Package eventbus also defines the canonical set of event kind strings
// appended to the events table. Keeping these as exported constants in a
// single file lets handlers, MCP tools and tests refer to them by symbol
// instead of string literals scattered across the tree.
//
// Adding a new kind: pick a dotted name in the existing namespaces
// (task.*, signal.*) and add it here before any handler emits it.
package eventbus

// Kind is the canonical type of an entry in the events table. The
// underlying type is string so it can be assigned directly to
// [Event.Type]; constants are grouped by entity below.
type Kind = string

// Task lifecycle and metadata events. These cover create / update /
// soft-disable as well as the various sub-collections (comments,
// attachments, actors, dependencies, constraints).
const (
	// TaskCreated is appended whenever a new task row is inserted.
	TaskCreated Kind = "task.created"
	// TaskUpdated is appended on any field-level patch to a task.
	TaskUpdated Kind = "task.updated"
	// TaskDisabled is appended when a task is soft-disabled (DELETE).
	TaskDisabled Kind = "task.disabled"

	// TaskCommentAdded is appended when a comment is created on a task.
	TaskCommentAdded Kind = "task.comment.added"
	// TaskCommentEdited is appended when a comment body is updated.
	TaskCommentEdited Kind = "task.comment.edited"
	// TaskCommentRemoved is appended when a comment is soft-removed.
	TaskCommentRemoved Kind = "task.comment.removed"

	// TaskAttachmentAdded is appended when an attachment is uploaded.
	TaskAttachmentAdded Kind = "task.attachment.added"
	// TaskAttachmentRemoved is appended when an attachment is removed.
	TaskAttachmentRemoved Kind = "task.attachment.removed"

	// TaskActorAdded is appended when an actor is bound to a task.
	TaskActorAdded Kind = "task.actor.added"
	// TaskActorRemoved is appended when an actor is unbound from a task.
	TaskActorRemoved Kind = "task.actor.removed"

	// TaskDependencyAdded is appended when a dependency edge is created.
	TaskDependencyAdded Kind = "task.dependency.added"
	// TaskDependencyRemoved is appended when a dependency edge is removed.
	TaskDependencyRemoved Kind = "task.dependency.removed"

	// TaskConstraintAdded is appended when a constraint row is attached.
	TaskConstraintAdded Kind = "task.constraint.added"
	// TaskConstraintRemoved is appended when a constraint row is removed.
	TaskConstraintRemoved Kind = "task.constraint.removed"
)

// Task transition events. The wire format is "task.transition.<name>"
// where <name> matches the transition keyword the actor invoked. Use
// [TaskTransition] to build a value from a free-form name when needed.
const (
	// TaskTransitionStart marks a task moving into the in-progress state.
	TaskTransitionStart Kind = "task.transition.start"
	// TaskTransitionBlock marks a task being explicitly blocked.
	TaskTransitionBlock Kind = "task.transition.block"
	// TaskTransitionUnblock marks a previously blocked task resuming.
	TaskTransitionUnblock Kind = "task.transition.unblock"
	// TaskTransitionSubmit marks a task being submitted for review.
	TaskTransitionSubmit Kind = "task.transition.submit"
	// TaskTransitionComplete marks a task being completed.
	TaskTransitionComplete Kind = "task.transition.complete"
	// TaskTransitionReopen marks a completed/cancelled task reopening.
	TaskTransitionReopen Kind = "task.transition.reopen"
	// TaskTransitionCancel marks a task being cancelled.
	TaskTransitionCancel Kind = "task.transition.cancel"
)

// TaskTransition builds a "task.transition.<name>" event kind from a
// free-form transition name. Handlers that already validate the
// transition name against an enum should prefer the explicit constants
// above; this helper exists so the transition handler does not need a
// switch statement to convert.
func TaskTransition(name string) Kind {
	return "task.transition." + name
}

// Signal events.
const (
	// SignalAttached is appended when an external signal (GitHub,
	// Slack, etc.) is bound to a task or surfaces in the inbox.
	SignalAttached Kind = "signal.attached"
)

// AI suggestion lifecycle events. Per ADR 0002 (events in MySQL, not a
// dedicated ai_suggestions table), AI proposals live in the same
// append-only events table as the rest of the system. payload_json
// carries the suggestion-specific shape (inbox_item_id, score, action,
// reasoning, ...). Handlers append "proposed" when the orchestrator
// returns a suggestion and "applied" / "dismissed" / "edited" when the
// user reacts to it from the Glass Dock.
const (
	// AiSuggestionProposed is appended when the orchestrator emits a
	// new AI suggestion to the user.
	AiSuggestionProposed Kind = "ai.suggestion.proposed"
	// AiSuggestionApplied is appended when the user accepts and runs
	// the recommended action.
	AiSuggestionApplied Kind = "ai.suggestion.applied"
	// AiSuggestionDismissed is appended when the user rejects the
	// suggestion outright.
	AiSuggestionDismissed Kind = "ai.suggestion.dismissed"
	// AiSuggestionEdited is appended when the user accepts the
	// suggestion after modifying its parameters.
	AiSuggestionEdited Kind = "ai.suggestion.edited"
)

// AI agent lifecycle events (4.AGENT-3 kill switch audit trail).
const (
	// AiAgentPaused is appended when an operator flips the kill switch
	// on an ai_agents row via POST /workspaces/{wsId}/ai/agents/{id}/pause.
	AiAgentPaused Kind = "ai.agent.paused"
	// AiAgentResumed is appended when the kill switch is released.
	AiAgentResumed Kind = "ai.agent.resumed"
	// AiAgentRunStarted is appended when a worker claims an agent_runs
	// row and begins execution.
	AiAgentRunStarted Kind = "ai.agent.run.started"
	// AiAgentRunCompleted is appended when a worker finishes an agent
	// run successfully.
	AiAgentRunCompleted Kind = "ai.agent.run.completed"
	// AiAgentRunFailed is appended when a worker's agent run returns
	// an error.
	AiAgentRunFailed Kind = "ai.agent.run.failed"
)

// Timebox lifecycle events.
const (
	// TimeboxCreated is appended when a new timebox is created.
	TimeboxCreated Kind = "timebox.created"
	// TimeboxUpdated is appended when a timebox is edited.
	TimeboxUpdated Kind = "timebox.updated"
	// TimeboxActivated is appended when a timebox transitions to active.
	TimeboxActivated Kind = "timebox.activated"
	// TimeboxCompleted is appended when a timebox transitions to completed.
	TimeboxCompleted Kind = "timebox.completed"
	// TimeboxTaskAdded is appended when a task is added to a timebox.
	TimeboxTaskAdded Kind = "timebox.task.added"
	// TimeboxTaskRemoved is appended when a task is removed from a timebox.
	TimeboxTaskRemoved Kind = "timebox.task.removed"
)

// Relation suggestion events.
const (
	// RelationSuggested is appended when the AI pipeline creates a
	// new relation suggestion between two tasks.
	RelationSuggested Kind = "relation.suggested"
	// RelationAccepted is appended when a user accepts a suggestion.
	RelationAccepted Kind = "relation.accepted"
	// RelationDismissed is appended when a user dismisses a suggestion.
	RelationDismissed Kind = "relation.dismissed"
)

// Lens sharing events.
const (
	// LensShared is appended when a lens is published publicly.
	LensShared Kind = "lens.shared"
	// LensUnshared is appended when a lens is taken private.
	LensUnshared Kind = "lens.unshared"
)

// Export events.
const (
	// ExportRequested is appended when a user requests an export.
	ExportRequested Kind = "export.requested"
)

// Legacy / compatibility kinds. These are kept so historical events
// continue to round-trip even though new code should not emit them.
const (
	// CommentAddedLegacy is the pre-namespacing alias for
	// [TaskCommentAdded] still emitted by one MCP tool path.
	CommentAddedLegacy Kind = "comment.added"
)
