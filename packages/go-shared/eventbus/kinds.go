// Package eventbus holds the canonical set of event-kind strings
// appended to the shared `events` table across all nodate-flow services.
//
// The strings live in a shared package so flow-api, auth-api and
// future services (itemkit, reconciler, memberkit) can all refer to
// the same symbols. Service-local packages (e.g.
// apps/flow-api/internal/eventbus) re-export these constants for
// backward compatibility — new code should import this package
// directly as `sharedbus`.
//
// Adding a new kind: pick a dotted name in the existing namespaces
// (task.*, calendar.*, item.*, signal.*, ai.*, ...) and add it here
// before any handler emits it. A kind also has to resolve to a
// [Family] (see registry.go) — that is what routes it to the SSE
// stream and the notification fan-out, and the totality check refuses
// a constant no family covers.
package eventbus

// Kind is the canonical type of an entry in the events table.
// Constants are grouped by entity below.
//
// It is a defined type rather than an alias for string so that a raw
// string cannot be used as an event kind by accident: a typo or an
// ad-hoc literal produces an event nobody consumes, and the failure
// only shows up as a missing notification much later. Writing an
// event kind therefore requires either one of the constants below or
// an explicit conversion. Conversions back to string are needed where
// the value leaves Go — the `type` column, JSON, log attributes — and
// those are spelled out at the boundary rather than hidden behind
// driver.Valuer / sql.Scanner on Kind, which would let any string
// flow back in unchecked.
type Kind string

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
	return Kind("task.transition." + name)
}

// Task events driven by the signaljudge Applier .
// These are emitted exclusively from apps/flow-api/internal/ai/signaljudge
// in response to a judged signal — never from generic task handlers.
const (
	// TaskAutoCompleted is appended when the Applier completes a task
	// because a signal verdict with action=complete_task was applied
	// under autonomy=auto. The derived_state collapses to "completed"
	// via the standard event projection (no direct UPDATE).
	TaskAutoCompleted Kind = "task.auto_completed"
	// TaskRetroDrafted is appended when the Applier creates a draft
	// retrospective task in response to a verdict with
	// action=generate_retro.
	TaskRetroDrafted Kind = "task.retro.drafted"
)

// Signal events. SignalAttached is appended by the public signal
// ingestion endpoint; the remaining kinds are emitted exclusively by
// the signaljudge Applier as it translates a
// judge verdict into concrete task-level effects.
const (
	// SignalAttached is appended when an external signal is ingested
	// (webhook, MCP, CLI) and persisted to the signals table.
	SignalAttached Kind = "signal.attached"
	// SignalJudged is appended every time a signal_judge agent run
	// produces a verdict, regardless of whether the verdict is applied.
	// Acts as the audit anchor for the judge loop.
	SignalJudged Kind = "signal.judged"
	// SignalApplied is appended when the Applier reifies a judge verdict
	// into a downstream task event under autonomy=auto.
	SignalApplied Kind = "signal.applied"
	// SignalRejected is appended when the Matcher or judge refuses a
	// signal with sufficient confidence (schema violation, low score,
	// explicit drop verdict) and no further action is taken.
	SignalRejected Kind = "signal.rejected"
)

// AI suggestion lifecycle events.
const (
	AiSuggestionProposed  Kind = "ai.suggestion.proposed"
	AiSuggestionApplied   Kind = "ai.suggestion.applied"
	AiSuggestionDismissed Kind = "ai.suggestion.dismissed"
	AiSuggestionEdited    Kind = "ai.suggestion.edited"
)

// AI auto-action events. The auto-action executor proposes a change it
// could apply and records the proposal without touching the task, so the
// activity feed shows what the rule engine wanted before anything acted
// on it.
const (
	AiAutoActionProposed Kind = "ai.auto_action.proposed"
)

// AI agent lifecycle events (kill switch audit trail).
const (
	AiAgentPaused       Kind = "ai.agent.paused"
	AiAgentResumed      Kind = "ai.agent.resumed"
	AiAgentRunStarted   Kind = "ai.agent.run.started"
	AiAgentRunCompleted Kind = "ai.agent.run.completed"
	AiAgentRunFailed    Kind = "ai.agent.run.failed"
)

// Agent handoff events. Emitted as an AI agent is attached to a task,
// records its private reasoning, and hands work back to a human or to
// another agent. Distinct from the run-level ai.agent.* kinds above:
// these are scoped to a single (agent, task) pair and form the audit
// trail surfaced on the task timeline.
const (
	// AgentTaskAttached is appended when an agent is assigned to a task
	// (task_actors row inserted with actor_agent_id set).
	AgentTaskAttached Kind = "agent.task.attached"
	// AgentTaskDetached is appended when an agent is removed from a task,
	// either explicitly or as part of a handoff to a human/another agent.
	AgentTaskDetached Kind = "agent.task.detached"
	// AgentTaskThought is appended when an agent records a private memo
	// (reasoning, plan, scratchpad) against a task it owns.
	AgentTaskThought Kind = "agent.task.thought"
	// AgentTaskHandoffToUser is appended when an agent hands a task back
	// to a human actor (typically because it needs input or has finished
	// its slice of work).
	AgentTaskHandoffToUser Kind = "agent.task.handoff_to_user"
	// AgentTaskHandoffToAgent is appended when an agent transfers a task
	// to another agent (delegation chain).
	AgentTaskHandoffToAgent Kind = "agent.task.handoff_to_agent"
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

	// CalendarSubscribed is appended when a user starts following a
	// calendar they do not own; CalendarSubscriptionUpdated when they
	// change how it is displayed to them (colour, visibility, alerts).
	CalendarSubscribed          Kind = "calendar.subscribed"
	CalendarSubscriptionUpdated Kind = "calendar.subscription.updated"

	CalEventCreated Kind = "calendar.event.created"
	CalEventUpdated Kind = "calendar.event.updated"
	CalEventDeleted Kind = "calendar.event.deleted"

	// Calendar membership reuses the added / removed / role_changed
	// vocabulary of workspace.member.*, so a reader of the log does not
	// have to learn two words for the same change.
	CalMemberAdded       Kind = "calendar.member.added"
	CalMemberRemoved     Kind = "calendar.member.removed"
	CalMemberRoleChanged Kind = "calendar.member.role_changed"

	CalMemoCreated   Kind = "calendar.memo.created"
	CalMemoUpdated   Kind = "calendar.memo.updated"
	CalMemoCompleted Kind = "calendar.memo.completed"
	CalMemoDeleted   Kind = "calendar.memo.deleted"

	// CalendarReminder is appended by the reminder scheduler when an
	// occurrence's notification window opens and this process wins the
	// claim for that (event, occurrence). It is time-driven: the row
	// carries actor_system_source rather than an actor, and its id anchors
	// the notification rows the reminder fans out to. The name has no
	// `event.` segment because the reminder is about the occurrence, which
	// for a series is not a row of its own.
	CalendarReminder Kind = "calendar.reminder"
)

// Calendar event detail events — the comments, attachments, checklist
// items, attendees and invites that hang off a single calendar event.
// They carry the event's public id in the payload; the subject of the
// row is still the calendar, so they share the calendar.* namespace.
const (
	CalEventCommentCreated Kind = "calendar.event.comment.created"
	CalEventCommentUpdated Kind = "calendar.event.comment.updated"
	CalEventCommentDeleted Kind = "calendar.event.comment.deleted"

	CalEventAttachmentCreated Kind = "calendar.event.attachment.created"
	CalEventAttachmentDeleted Kind = "calendar.event.attachment.deleted"

	CalEventChecklistCreated Kind = "calendar.event.checklist.created"
	CalEventChecklistUpdated Kind = "calendar.event.checklist.updated"
	CalEventChecklistDeleted Kind = "calendar.event.checklist.deleted"

	// Attendee changes are named for the change, not for how many rows
	// one request happened to touch: the bulk add carries a count in its
	// payload and still appends CalEventAttendeeAdded, so the pair reads
	// the same way as task.actor.added / task.actor.removed.
	CalEventAttendeeAdded   Kind = "calendar.event.attendee.added"
	CalEventAttendeeRemoved Kind = "calendar.event.attendee.removed"
	CalEventRsvpUpdated     Kind = "calendar.event.rsvp.updated"

	CalEventInviteCreated Kind = "calendar.event.invite.created"
	CalEventInviteRotated Kind = "calendar.event.invite.rotated"
	CalEventInviteRevoked Kind = "calendar.event.invite.revoked"
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

// Public share events — calendar_public_shares. The namespace is
// public_share.* rather than share.*: these rows are what the handlers
// have always written, so the log's history and the constants name the
// same thing.
const (
	PublicShareCreated Kind = "public_share.created"
	PublicShareUpdated Kind = "public_share.updated"
	// PublicShareRotated is appended when the share's token is replaced,
	// which revokes every URL handed out so far.
	PublicShareRotated Kind = "public_share.rotated"
	PublicShareDeleted Kind = "public_share.deleted"

	// The attach and reorder kinds are plural because one request works
	// on the share's whole event list; detach names a single event.
	PublicShareEventsAttached  Kind = "public_share.events_attached"
	PublicShareEventsReordered Kind = "public_share.events_reordered"
	PublicShareEventDetached   Kind = "public_share.event_detached"
)

// Legacy / compatibility kinds. Kept so historical events continue to
// round-trip even though new code should not emit them.
const (
	CommentAddedLegacy Kind = "comment.added"
)
