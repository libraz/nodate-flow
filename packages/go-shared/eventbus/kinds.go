// Package eventbus holds the canonical set of event-kind strings
// appended to the shared `events` table across all nodate-flow services.
//
// The strings live in a shared package so flow-api, time-api and
// future services (itemkit, reconciler, memberkit) can all refer to
// the same symbols. Service-local packages (e.g.
// apps/flow-api/internal/eventbus) re-export these constants for
// backward compatibility — new code should import this package
// directly as `sharedbus`.
//
// Adding a new kind: pick a dotted name in the existing namespaces
// (task.*, calendar.*, item.*, signal.*, ai.*, ...) and add it here
// before any handler emits it.
package eventbus

// Kind is the canonical type of an entry in the events table. The
// underlying type is string so it can be assigned directly to any
// `type` column; constants are grouped by entity below.
type Kind = string

// Task lifecycle and metadata events.
const (
	TaskCreated  Kind = "task.created"
	TaskUpdated  Kind = "task.updated"
	TaskDisabled Kind = "task.disabled"

	TaskCommentAdded   Kind = "task.comment.added"
	TaskCommentEdited  Kind = "task.comment.edited"
	TaskCommentRemoved Kind = "task.comment.removed"

	TaskAttachmentAdded   Kind = "task.attachment.added"
	TaskAttachmentRemoved Kind = "task.attachment.removed"

	TaskActorAdded   Kind = "task.actor.added"
	TaskActorRemoved Kind = "task.actor.removed"

	TaskDependencyAdded   Kind = "task.dependency.added"
	TaskDependencyRemoved Kind = "task.dependency.removed"

	TaskConstraintAdded   Kind = "task.constraint.added"
	TaskConstraintRemoved Kind = "task.constraint.removed"
)

// Label events.
const (
	LabelCreated  Kind = "label.created"
	LabelUpdated  Kind = "label.updated"
	LabelDisabled Kind = "label.disabled"

	TaskLabelAdded   Kind = "task.label.added"
	TaskLabelRemoved Kind = "task.label.removed"
)

// Archive events.
const (
	TaskArchived   Kind = "task.archived"
	TaskUnarchived Kind = "task.unarchived"

	PageArchived   Kind = "page.archived"
	PageUnarchived Kind = "page.unarchived"

	LensArchived Kind = "lens.archived"

	TimeboxArchived Kind = "timebox.archived"
)

// Task transition events. The wire format is "task.transition.<name>".
const (
	TaskTransitionStart    Kind = "task.transition.start"
	TaskTransitionBlock    Kind = "task.transition.block"
	TaskTransitionUnblock  Kind = "task.transition.unblock"
	TaskTransitionSubmit   Kind = "task.transition.submit"
	TaskTransitionComplete Kind = "task.transition.complete"
	TaskTransitionReopen   Kind = "task.transition.reopen"
	TaskTransitionCancel   Kind = "task.transition.cancel"
)

// TaskTransition builds a "task.transition.<name>" event kind from a
// free-form transition name.
func TaskTransition(name string) Kind {
	return "task.transition." + name
}

// Signal events.
const (
	SignalAttached Kind = "signal.attached"
)

// AI suggestion lifecycle events.
const (
	AiSuggestionProposed  Kind = "ai.suggestion.proposed"
	AiSuggestionApplied   Kind = "ai.suggestion.applied"
	AiSuggestionDismissed Kind = "ai.suggestion.dismissed"
	AiSuggestionEdited    Kind = "ai.suggestion.edited"
)

// AI agent lifecycle events (kill switch audit trail).
const (
	AiAgentPaused       Kind = "ai.agent.paused"
	AiAgentResumed      Kind = "ai.agent.resumed"
	AiAgentRunStarted   Kind = "ai.agent.run.started"
	AiAgentRunCompleted Kind = "ai.agent.run.completed"
	AiAgentRunFailed    Kind = "ai.agent.run.failed"
)

// Timebox lifecycle events.
const (
	TimeboxCreated     Kind = "timebox.created"
	TimeboxUpdated     Kind = "timebox.updated"
	TimeboxActivated   Kind = "timebox.activated"
	TimeboxCompleted   Kind = "timebox.completed"
	TimeboxTaskAdded   Kind = "timebox.task.added"
	TimeboxTaskRemoved Kind = "timebox.task.removed"
)

// Relation suggestion events.
const (
	RelationSuggested Kind = "relation.suggested"
	RelationAccepted  Kind = "relation.accepted"
	RelationDismissed Kind = "relation.dismissed"
)

// Lens sharing events.
const (
	LensShared   Kind = "lens.shared"
	LensUnshared Kind = "lens.unshared"
)

// Page lifecycle events.
const (
	PageCreated  Kind = "page.created"
	PageUpdated  Kind = "page.updated"
	PageDisabled Kind = "page.disabled"
)

// Dashboard widget lifecycle events.
const (
	DashboardWidgetCreated  Kind = "dashboard.widget.created"
	DashboardWidgetUpdated  Kind = "dashboard.widget.updated"
	DashboardWidgetDisabled Kind = "dashboard.widget.disabled"
)

// Export events.
const (
	ExportRequested Kind = "export.requested"
)

// Calendar lifecycle events.
const (
	CalendarCreated Kind = "calendar.created"
	CalendarUpdated Kind = "calendar.updated"
	CalendarDeleted Kind = "calendar.deleted"

	CalEventCreated Kind = "calendar.event.created"
	CalEventUpdated Kind = "calendar.event.updated"
	CalEventDeleted Kind = "calendar.event.deleted"

	CalMemberJoined Kind = "calendar.member.joined"
	CalMemberLeft   Kind = "calendar.member.left"

	CalMemoCreated   Kind = "calendar.memo.created"
	CalMemoCompleted Kind = "calendar.memo.completed"
)

// Reaction events.
const (
	ReactionAdded   Kind = "reaction.added"
	ReactionRemoved Kind = "reaction.removed"
)

// Mention events.
const (
	MentionCreated Kind = "mention.created"
)

// Favorite events.
const (
	FavoriteAdded   Kind = "favorite.added"
	FavoriteRemoved Kind = "favorite.removed"
)

// Intake events.
const (
	IntakeItemCreated   Kind = "intake.item.created"
	IntakeItemAccepted  Kind = "intake.item.accepted"
	IntakeItemRejected  Kind = "intake.item.rejected"
	IntakeItemSnoozed   Kind = "intake.item.snoozed"
	IntakeItemDuplicate Kind = "intake.item.duplicate"
)

// Description version events.
const (
	DescriptionVersionCreated  Kind = "description.version.created"
	DescriptionVersionRestored Kind = "description.version.restored"
)

// Import job events.
const (
	ImportJobCreated   Kind = "import.job.created"
	ImportJobCompleted Kind = "import.job.completed"
	ImportJobFailed    Kind = "import.job.failed"
	ImportJobCancelled Kind = "import.job.cancelled"
)

// Workspace membership lifecycle events (emitted by memberkit).
const (
	WorkspaceMemberAdded       Kind = "workspace.member.added"
	WorkspaceMemberRemoved     Kind = "workspace.member.removed"
	WorkspaceMemberRoleChanged Kind = "workspace.member.role_changed"
)

// Item (unified task+event) lifecycle events.
//
// These fire whenever itemkit mutates the linked (task ↔ calendar_event)
// pair atomically. During the dual-append transition window, itemkit
// ALSO emits the legacy `task.*` and `calendar.event.*` kinds so
// existing webhook subscribers and notification consumers keep working.
// The legacy parallel emission is removed once all subscribers have
// migrated off the deprecated kinds.
const (
	// ItemScheduled is appended when a task gains a linked calendar
	// event (1:1 projection via calendar_events.task_id).
	ItemScheduled Kind = "item.scheduled"
	// ItemUnscheduled is appended when the projection link is removed
	// while the task itself survives.
	ItemUnscheduled Kind = "item.unscheduled"
	// ItemRescheduled is appended when a date/time change propagates
	// through the link in either direction.
	ItemRescheduled Kind = "item.rescheduled"
	// ItemRenamed is appended when a title change propagates through
	// the link in either direction.
	ItemRenamed Kind = "item.renamed"
	// ItemDeleted is appended when a cascade delete removes both the
	// task and every linked event in one transaction.
	ItemDeleted Kind = "item.deleted"
	// ItemReconciled is appended by the reconciler when it
	// self-heals a drift between a task and its linked event(s).
	ItemReconciled Kind = "item.reconciled"

	// ItemActorAdded is appended when itemkit propagates a task_actors
	// insert to calendar_event_attendees rows on every linked event.
	ItemActorAdded Kind = "item.actor.added"
	// ItemActorRemoved is appended when itemkit propagates a task_actors
	// soft-remove to the corresponding attendee rows.
	ItemActorRemoved Kind = "item.actor.removed"
	// ItemVisibilityChanged is appended when a task.visibility patch
	// propagates to every linked event's visibility column.
	ItemVisibilityChanged Kind = "item.visibility.changed"

	// ItemMilestoneLinkAdded is appended when a task is linked to a
	// kind='milestone' event via task_event_links.
	ItemMilestoneLinkAdded Kind = "item.milestone.link.added"
	// ItemMilestoneLinkRemoved is appended when a task↔milestone link
	// is soft-removed from task_event_links.
	ItemMilestoneLinkRemoved Kind = "item.milestone.link.removed"
)

// Public share events — calendar_public_shares.
const (
	SharePublished      Kind = "share.published"
	ShareUpdated        Kind = "share.updated"
	ShareTokenRotated   Kind = "share.token.rotated"
	ShareDeleted        Kind = "share.deleted"
	ShareEventAttached  Kind = "share.event.attached"
	ShareEventDetached  Kind = "share.event.detached"
)

// Legacy / compatibility kinds. Kept so historical events continue to
// round-trip even though new code should not emit them.
const (
	CommentAddedLegacy Kind = "comment.added"
)
