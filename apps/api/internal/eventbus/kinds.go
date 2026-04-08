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

// Legacy / compatibility kinds. These are kept so historical events
// continue to round-trip even though new code should not emit them.
const (
	// CommentAddedLegacy is the pre-namespacing alias for
	// [TaskCommentAdded] still emitted by one MCP tool path.
	CommentAddedLegacy Kind = "comment.added"
)
