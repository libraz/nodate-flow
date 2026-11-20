package notification

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification/prefs"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// classification is what a notification row says about one event kind.
// A zero Title means the kind notifies nobody.
//
// It is the one place a kind declares everything the fan-out does with
// it: the copy, the resource the row links to (the event's task, the only
// public id the event row supplies), who it is for, and whether its
// recipients are read from a task that is already disabled.
type classification struct {
	Title        string
	ResourceType string
	Severity     generated.NotificationsSeverity
	// To says who the notification is for; see [audience].
	To audience
	// Deletion marks a kind appended after its task was disabled, whose
	// readers are therefore resolved without the enabled filter.
	Deletion bool
}

// audience is who a notifying kind addresses.
type audience int

const (
	// audienceReaders announces to every member who may read the resource.
	audienceReaders audience = iota
	// audienceMentioned addresses the users a mention payload names.
	audienceMentioned
	// audienceTaskActor addresses the user behind the payload's actorId.
	audienceTaskActor
	// audienceRemovedTaskActor addresses that user even though the removal
	// already took their access away.
	audienceRemovedTaskActor
	// audienceTargetUser addresses the payload's targetUserPublicId or,
	// when the handoff names nobody, the task's assignees (else creator) —
	// the people it is handed back to, never every reader.
	audienceTargetUser
	// Workflow decisions have an explicit recipient; a removed account
	// must not turn a personal notification into one for other task actors.
	audienceRequiredTargetUser
)

// silent is the classification of a kind that deliberately notifies
// nobody. Written out rather than left as a missing map entry, because
// "nothing to say about this" and "nobody has decided yet" are different
// answers and the table has to tell them apart.
var silent = classification{}

// notify builds the classification of a kind that does notify, announced
// to every reader of its resource.
func notify(title, resourceType string, severity generated.NotificationsSeverity) classification {
	return classification{Title: title, ResourceType: resourceType, Severity: severity}
}

// to narrows a notifying classification to an addressed audience.
func (c classification) to(a audience) classification {
	c.To = a
	return c
}

// onDeletion marks a notifying classification as a deletion kind.
func (c classification) onDeletion() classification {
	c.Deletion = true
	return c
}

// classifications maps every declared event kind onto its notification
// copy. It is total over [eventbus.Kinds]: a kind with no entry fails
// TestEveryKindIsClassified rather than reaching fan-out and being
// dropped by a default branch.
//
// That default branch is what this table replaces. It answered "do not
// notify" for a kind nobody had classified yet, so adding an event kind
// — or renaming one — produced a feature that worked everywhere except
// the notification bell, with nothing anywhere to say so. Most kinds
// here are [silent], and that is fine; what matters is that each one was
// looked at.
var classifications = map[eventbus.Kind]classification{
	eventbus.TaskCreated: notify("A new task was created", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskUpdated: notify("A task was updated", "task", generated.NotificationsSeverityLow),
	// Only ever appended as the legacy twin of item.deleted, inside the
	// same transaction; item.deleted carries the notification so one
	// delete notifies each reader once.
	eventbus.TaskDisabled: silent,

	// The row links the task the comment is on: the event row supplies the
	// task's public id, not the comment's.
	eventbus.TaskCommentAdded:   notify("A new comment was added", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskCommentEdited:  notify("A comment was edited", "task", generated.NotificationsSeverityLow),
	eventbus.TaskCommentRemoved: notify("A comment was removed", "task", generated.NotificationsSeverityLow),

	eventbus.TaskActorAdded:        notify("You were added to a task", "task", generated.NotificationsSeverityNormal).to(audienceTaskActor),
	eventbus.TaskActorRemoved:      notify("You were removed from a task", "task", generated.NotificationsSeverityNormal).to(audienceRemovedTaskActor),
	eventbus.TaskSubmissionCreated: notify("Work is ready for your review", "task", generated.NotificationsSeverityNormal).to(audienceRequiredTargetUser),
	eventbus.TaskReviewDecided:     notify("Your submitted work was reviewed", "task", generated.NotificationsSeverityNormal).to(audienceRequiredTargetUser),
	eventbus.TaskHandoffRequested:  notify("A task handoff needs your response", "task", generated.NotificationsSeverityHigh).to(audienceRequiredTargetUser),
	eventbus.TaskHandoffDecided:    notify("Your task handoff was resolved", "task", generated.NotificationsSeverityNormal).to(audienceRequiredTargetUser),

	eventbus.TaskTransitionStart:    notify("A task was started", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionComplete: notify("A task was completed", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionBlock:    notify("A task was blocked", "task", generated.NotificationsSeverityHigh),
	eventbus.TaskTransitionUnblock:  notify("A task was unblocked", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionSubmit:   notify("A task was submitted for review", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionReopen:   notify("A task was reopened", "task", generated.NotificationsSeverityNormal),
	eventbus.TaskTransitionCancel:   notify("A task was cancelled", "task", generated.NotificationsSeverityNormal),

	// itemkit kinds: task ↔ calendar_event atomic mutations. The reader
	// for these events is the "item" (task + its projections);
	// resourceType stays "task" because downstream routing is the same.
	eventbus.ItemScheduled:            notify("An item was placed on a calendar", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemUnscheduled:          notify("An item was removed from a calendar", "task", generated.NotificationsSeverityLow),
	eventbus.ItemRescheduled:          notify("An item was rescheduled", "task", generated.NotificationsSeverityNormal),
	eventbus.ItemRenamed:              notify("An item was renamed", "task", generated.NotificationsSeverityLow),
	eventbus.ItemDeleted:              notify("An item was deleted", "task", generated.NotificationsSeverityNormal).onDeletion(),
	eventbus.ItemReconciled:           notify("An item was auto-reconciled", "task", generated.NotificationsSeverityLow),
	eventbus.ItemActorAdded:           notify("You were added to an item", "task", generated.NotificationsSeverityNormal).to(audienceTaskActor),
	eventbus.ItemActorRemoved:         notify("You were removed from an item", "task", generated.NotificationsSeverityNormal).to(audienceRemovedTaskActor),
	eventbus.ItemVisibilityChanged:    notify("An item's visibility changed", "task", generated.NotificationsSeverityLow),
	eventbus.ItemMilestoneLinkAdded:   notify("An item was linked to a milestone", "task", generated.NotificationsSeverityLow),
	eventbus.ItemMilestoneLinkRemoved: notify("An item was unlinked from a milestone", "task", generated.NotificationsSeverityLow),

	// agent.task.* events: AI agent lifecycle on tasks. handoff_to_user is
	// the user-facing one that the inbox badge must surface; thought is
	// silent because it records private agent reasoning.
	eventbus.AgentTaskHandoffToUser:  notify("An agent handed back to you", "task", generated.NotificationsSeverityHigh).to(audienceTargetUser),
	eventbus.AgentTaskHandoffToAgent: notify("A task was handed off to an agent", "task", generated.NotificationsSeverityNormal),
	eventbus.AgentTaskAttached:       notify("An agent was attached to a task", "task", generated.NotificationsSeverityLow),
	eventbus.AgentTaskDetached:       notify("An agent was detached from a task", "task", generated.NotificationsSeverityLow),
	eventbus.AgentTaskThought:        silent,

	// Task metadata: the task itself carries the change and the lists
	// refresh off the stream, so a per-recipient row would be noise.
	eventbus.TaskAttachmentAdded:     silent,
	eventbus.TaskAttachmentRemoved:   silent,
	eventbus.TaskDependencyAdded:     silent,
	eventbus.TaskDependencyRemoved:   silent,
	eventbus.TaskConstraintAdded:     silent,
	eventbus.TaskConstraintRemoved:   silent,
	eventbus.TaskConstraintSatisfied: silent,
	eventbus.TaskConstraintFailed:    silent,
	eventbus.TaskLabelAdded:          silent,
	eventbus.TaskLabelRemoved:        silent,
	eventbus.TaskArchived:            silent,
	eventbus.TaskUnarchived:          silent,

	// Judge-driven task effects. The signal timeline is where these are
	// read; a notification would duplicate the transition row the Applier
	// also appends.
	eventbus.TaskAutoCompleted: silent,
	eventbus.TaskRetroDrafted:  silent,

	// Signal lifecycle: machine-facing audit anchors for the judge loop.
	eventbus.SignalAttached: silent,
	eventbus.SignalJudged:   silent,
	eventbus.SignalApplied:  silent,
	eventbus.SignalRejected: silent,

	// Inbox triage carries no audience of its own: the queue belongs to
	// the workspace rather than to a reader, so there is nobody the bell
	// would name, and the change is read from the inbox the actor is
	// already looking at. Same reasoning as the intake kinds below, which
	// are the same two verbs on the other triage surface.
	eventbus.SignalArchived: silent,
	eventbus.SignalSnoozed:  silent,

	// AI suggestions and agent runs surface in their own screens, which
	// the stream keeps fresh.
	eventbus.AiSuggestionProposed:  silent,
	eventbus.AiSuggestionApplied:   silent,
	eventbus.AiSuggestionDismissed: silent,
	eventbus.AiSuggestionEdited:    silent,
	// A proposal the executor has not acted on is a note on the activity
	// feed, not something to interrupt anyone with; the applied action
	// that may follow carries its own kind.
	eventbus.AiAutoActionProposed: silent,
	eventbus.AiAgentPaused:        silent,
	eventbus.AiAgentResumed:       silent,
	eventbus.AiAgentRunStarted:    silent,
	eventbus.AiAgentRunCompleted:  silent,
	eventbus.AiAgentRunFailed:     silent,

	// Workspace furniture: labels, pages, lenses, dashboards, timeboxes,
	// relations and exports are read from their own lists.
	eventbus.LabelCreated:            silent,
	eventbus.LabelUpdated:            silent,
	eventbus.LabelDisabled:           silent,
	eventbus.PageCreated:             silent,
	eventbus.PageUpdated:             silent,
	eventbus.PageDisabled:            silent,
	eventbus.PageArchived:            silent,
	eventbus.PageUnarchived:          silent,
	eventbus.LensCreated:             silent,
	eventbus.LensUpdated:             silent,
	eventbus.LensShared:              silent,
	eventbus.LensUnshared:            silent,
	eventbus.LensArchived:            silent,
	eventbus.DashboardWidgetCreated:  silent,
	eventbus.DashboardWidgetUpdated:  silent,
	eventbus.DashboardWidgetDisabled: silent,
	eventbus.TimeboxCreated:          silent,
	eventbus.TimeboxUpdated:          silent,
	eventbus.TimeboxActivated:        silent,
	eventbus.TimeboxCompleted:        silent,
	eventbus.TimeboxTaskAdded:        silent,
	eventbus.TimeboxTaskRemoved:      silent,
	eventbus.TimeboxArchived:         silent,
	eventbus.RelationSuggested:       silent,
	eventbus.RelationAccepted:        silent,
	eventbus.RelationDismissed:       silent,
	eventbus.ExportRequested:         silent,

	// Calendar surface. Calendar changes reach their readers through the
	// calendar itself rather than the notification bell.
	eventbus.CalendarCreated:             silent,
	eventbus.CalendarUpdated:             silent,
	eventbus.CalendarDeleted:             silent,
	eventbus.CalendarSubscribed:          silent,
	eventbus.CalendarSubscriptionUpdated: silent,
	eventbus.CalEventCreated:             silent,
	eventbus.CalEventUpdated:             silent,
	eventbus.CalEventDeleted:             silent,
	eventbus.CalMemberAdded:              silent,
	eventbus.CalMemberRemoved:            silent,
	eventbus.CalMemberRoleChanged:        silent,
	eventbus.CalMemoCreated:              silent,
	eventbus.CalMemoUpdated:              silent,
	eventbus.CalMemoCompleted:            silent,
	eventbus.CalMemoDeleted:              silent,

	// A reminder does notify, but not from this table: the scheduler calls
	// [Fanout.DeliverCalendarReminder] with the occurrence's own title, so
	// the row a recipient sees names the meeting rather than a fixed
	// sentence. This table is read by the hook fan-out only, which never
	// sees the kind, and a title here would be copy nothing renders.
	eventbus.CalendarReminder: silent,

	eventbus.CalEventCommentCreated:    silent,
	eventbus.CalEventCommentUpdated:    silent,
	eventbus.CalEventCommentDeleted:    silent,
	eventbus.CalEventAttachmentCreated: silent,
	eventbus.CalEventAttachmentDeleted: silent,
	eventbus.CalEventChecklistCreated:  silent,
	eventbus.CalEventChecklistUpdated:  silent,
	eventbus.CalEventChecklistDeleted:  silent,
	eventbus.CalEventAttendeeAdded:     silent,
	eventbus.CalEventAttendeeRemoved:   silent,
	eventbus.CalEventRsvpUpdated:       silent,
	eventbus.CalEventInviteCreated:     silent,
	eventbus.CalEventInviteRotated:     silent,
	eventbus.CalEventInviteRevoked:     silent,

	// Public shares are an owner-side administrative surface.
	eventbus.PublicShareCreated:         silent,
	eventbus.PublicShareUpdated:         silent,
	eventbus.PublicShareRotated:         silent,
	eventbus.PublicShareDeleted:         silent,
	eventbus.PublicShareEventsAttached:  silent,
	eventbus.PublicShareEventsReordered: silent,
	eventbus.PublicShareEventDetached:   silent,

	// Reactions and favourites are read from the row they are attached to.
	eventbus.ReactionAdded:   silent,
	eventbus.ReactionRemoved: silent,
	eventbus.FavoriteAdded:   silent,
	eventbus.FavoriteRemoved: silent,

	// A mention reaches the people the body names and nobody else; the
	// resource is the task the body was written on, which is the only
	// public id the event row can supply.
	eventbus.MentionCreated: notify("You were mentioned", "task", generated.NotificationsSeverityNormal).to(audienceMentioned),

	// Intake triage, description history and import jobs are read from
	// the screen that started them.
	eventbus.IntakeItemCreated:         silent,
	eventbus.IntakeItemAccepted:        silent,
	eventbus.IntakeItemRejected:        silent,
	eventbus.IntakeItemSnoozed:         silent,
	eventbus.IntakeItemDuplicate:       silent,
	eventbus.DescriptionVersionCreated: silent,

	eventbus.DescriptionVersionRestored: silent,
	eventbus.ImportJobCreated:           silent,
	eventbus.ImportJobCompleted:         silent,
	eventbus.ImportJobFailed:            silent,
	eventbus.ImportJobCancelled:         silent,

	// Workspace membership is handled by the invitation flow in auth-api.
	eventbus.WorkspaceMemberAdded:       silent,
	eventbus.WorkspaceMemberRemoved:     silent,
	eventbus.WorkspaceMemberRoleChanged: silent,

	// Historical rows only; nothing emits this any more.
	eventbus.CommentAddedLegacy: silent,
}

// categoryForEventType maps an eventbus event type (the dotted string
// emitted on the bus, e.g. "task.comment.added") to the broader
// notification_preferences.event_category that users configure in their
// preferences UI.
//
// The schema groups dozens of event types into a handful of stable
// categories ([prefs.Categories] is the canonical list). New event types fall
// back to the broadest task.lifecycle bucket so fan-out keeps working
// before a more specific bucket is added — which also means every
// category this returns must appear in [prefs.Categories], or the events
// routed to it would be unmutable.
func categoryForEventType(eventType string) string {
	switch eventType {
	case "task.comment.added", "task.comment.edited", "task.comment.removed":
		return prefs.CategoryTaskComment
	case "task.actor.added", "task.actor.removed",
		"item.actor.added", "item.actor.removed",
		"mention.created":
		return prefs.CategoryTaskMention
	case "item.scheduled", "item.unscheduled", "item.rescheduled":
		return prefs.CategoryTimebox
	case "item.milestone.link.added", "item.milestone.link.removed":
		return prefs.CategoryRelation
	case "agent.task.handoff_to_user", "agent.task.handoff_to_agent",
		"agent.task.attached", "agent.task.detached", "agent.task.thought":
		return prefs.CategoryAI
	default:
		return prefs.CategoryTaskLifecycle
	}
}
