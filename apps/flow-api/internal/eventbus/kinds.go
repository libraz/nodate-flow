// Package eventbus re-exports event-kind constants defined in the
// shared package so existing flow-api call sites continue to compile
// without churn. New code should import
// github.com/libraz/nodate-flow/packages/go-shared/eventbus
// directly. These aliases will be removed once the calendar
// unification cleanup has landed everywhere.
package eventbus

import sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"

// Kind mirrors sharedbus.Kind.
type Kind = sharedbus.Kind

// Task lifecycle and metadata events.
const (
	TaskCreated  = sharedbus.TaskCreated
	TaskUpdated  = sharedbus.TaskUpdated
	TaskDisabled = sharedbus.TaskDisabled

	TaskCommentAdded   = sharedbus.TaskCommentAdded
	TaskCommentEdited  = sharedbus.TaskCommentEdited
	TaskCommentRemoved = sharedbus.TaskCommentRemoved

	TaskAttachmentAdded   = sharedbus.TaskAttachmentAdded
	TaskAttachmentRemoved = sharedbus.TaskAttachmentRemoved

	TaskActorAdded   = sharedbus.TaskActorAdded
	TaskActorRemoved = sharedbus.TaskActorRemoved

	TaskDependencyAdded   = sharedbus.TaskDependencyAdded
	TaskDependencyRemoved = sharedbus.TaskDependencyRemoved

	TaskConstraintAdded   = sharedbus.TaskConstraintAdded
	TaskConstraintRemoved = sharedbus.TaskConstraintRemoved
)

// Label events.
const (
	LabelCreated  = sharedbus.LabelCreated
	LabelUpdated  = sharedbus.LabelUpdated
	LabelDisabled = sharedbus.LabelDisabled

	TaskLabelAdded   = sharedbus.TaskLabelAdded
	TaskLabelRemoved = sharedbus.TaskLabelRemoved
)

// Archive events.
const (
	TaskArchived   = sharedbus.TaskArchived
	TaskUnarchived = sharedbus.TaskUnarchived

	PageArchived   = sharedbus.PageArchived
	PageUnarchived = sharedbus.PageUnarchived

	LensArchived = sharedbus.LensArchived

	TimeboxArchived = sharedbus.TimeboxArchived
)

// Task transition events.
const (
	TaskTransitionStart    = sharedbus.TaskTransitionStart
	TaskTransitionBlock    = sharedbus.TaskTransitionBlock
	TaskTransitionUnblock  = sharedbus.TaskTransitionUnblock
	TaskTransitionSubmit   = sharedbus.TaskTransitionSubmit
	TaskTransitionComplete = sharedbus.TaskTransitionComplete
	TaskTransitionReopen   = sharedbus.TaskTransitionReopen
	TaskTransitionCancel   = sharedbus.TaskTransitionCancel
)

// TaskTransition delegates to the shared helper.
func TaskTransition(name string) Kind { return sharedbus.TaskTransition(name) }

// Task events driven by the signaljudge Applier.
const (
	TaskAutoCompleted = sharedbus.TaskAutoCompleted
	TaskRetroDrafted  = sharedbus.TaskRetroDrafted
)

// Signal events (ingestion + judge loop).
const (
	SignalAttached = sharedbus.SignalAttached
	SignalJudged   = sharedbus.SignalJudged
	SignalApplied  = sharedbus.SignalApplied
	SignalRejected = sharedbus.SignalRejected
)

// AI suggestion lifecycle events.
const (
	AiSuggestionProposed  = sharedbus.AiSuggestionProposed
	AiSuggestionApplied   = sharedbus.AiSuggestionApplied
	AiSuggestionDismissed = sharedbus.AiSuggestionDismissed
	AiSuggestionEdited    = sharedbus.AiSuggestionEdited
)

// AI agent lifecycle events.
const (
	AiAgentPaused       = sharedbus.AiAgentPaused
	AiAgentResumed      = sharedbus.AiAgentResumed
	AiAgentRunStarted   = sharedbus.AiAgentRunStarted
	AiAgentRunCompleted = sharedbus.AiAgentRunCompleted
	AiAgentRunFailed    = sharedbus.AiAgentRunFailed
)

// Agent handoff events (per-task attach / thought / handoff trail).
const (
	AgentTaskAttached       = sharedbus.AgentTaskAttached
	AgentTaskDetached       = sharedbus.AgentTaskDetached
	AgentTaskThought        = sharedbus.AgentTaskThought
	AgentTaskHandoffToUser  = sharedbus.AgentTaskHandoffToUser
	AgentTaskHandoffToAgent = sharedbus.AgentTaskHandoffToAgent
)

// Timebox lifecycle events.
const (
	TimeboxCreated     = sharedbus.TimeboxCreated
	TimeboxUpdated     = sharedbus.TimeboxUpdated
	TimeboxActivated   = sharedbus.TimeboxActivated
	TimeboxCompleted   = sharedbus.TimeboxCompleted
	TimeboxTaskAdded   = sharedbus.TimeboxTaskAdded
	TimeboxTaskRemoved = sharedbus.TimeboxTaskRemoved
)

// Relation suggestion events.
const (
	RelationSuggested = sharedbus.RelationSuggested
	RelationAccepted  = sharedbus.RelationAccepted
	RelationDismissed = sharedbus.RelationDismissed
)

// Lens sharing events.
const (
	LensShared   = sharedbus.LensShared
	LensUnshared = sharedbus.LensUnshared
)

// Page lifecycle events.
const (
	PageCreated  = sharedbus.PageCreated
	PageUpdated  = sharedbus.PageUpdated
	PageDisabled = sharedbus.PageDisabled
)

// Dashboard widget lifecycle events.
const (
	DashboardWidgetCreated  = sharedbus.DashboardWidgetCreated
	DashboardWidgetUpdated  = sharedbus.DashboardWidgetUpdated
	DashboardWidgetDisabled = sharedbus.DashboardWidgetDisabled
)

// Export events.
const (
	ExportRequested = sharedbus.ExportRequested
)

// Calendar lifecycle events.
const (
	CalendarCreated = sharedbus.CalendarCreated
	CalendarUpdated = sharedbus.CalendarUpdated
	CalendarDeleted = sharedbus.CalendarDeleted

	CalEventCreated = sharedbus.CalEventCreated
	CalEventUpdated = sharedbus.CalEventUpdated
	CalEventDeleted = sharedbus.CalEventDeleted

	CalMemberJoined = sharedbus.CalMemberJoined
	CalMemberLeft   = sharedbus.CalMemberLeft

	CalMemoCreated   = sharedbus.CalMemoCreated
	CalMemoCompleted = sharedbus.CalMemoCompleted
)

// Reaction events.
const (
	ReactionAdded   = sharedbus.ReactionAdded
	ReactionRemoved = sharedbus.ReactionRemoved
)

// Mention events.
const (
	MentionCreated = sharedbus.MentionCreated
)

// Favorite events.
const (
	FavoriteAdded   = sharedbus.FavoriteAdded
	FavoriteRemoved = sharedbus.FavoriteRemoved
)

// Intake events.
const (
	IntakeItemCreated   = sharedbus.IntakeItemCreated
	IntakeItemAccepted  = sharedbus.IntakeItemAccepted
	IntakeItemRejected  = sharedbus.IntakeItemRejected
	IntakeItemSnoozed   = sharedbus.IntakeItemSnoozed
	IntakeItemDuplicate = sharedbus.IntakeItemDuplicate
)

// Description version events.
const (
	DescriptionVersionCreated  = sharedbus.DescriptionVersionCreated
	DescriptionVersionRestored = sharedbus.DescriptionVersionRestored
)

// Import job events.
const (
	ImportJobCreated   = sharedbus.ImportJobCreated
	ImportJobCompleted = sharedbus.ImportJobCompleted
	ImportJobFailed    = sharedbus.ImportJobFailed
	ImportJobCancelled = sharedbus.ImportJobCancelled
)

// Workspace membership lifecycle events (emitted by memberkit).
const (
	WorkspaceMemberAdded       = sharedbus.WorkspaceMemberAdded
	WorkspaceMemberRemoved     = sharedbus.WorkspaceMemberRemoved
	WorkspaceMemberRoleChanged = sharedbus.WorkspaceMemberRoleChanged
)

// Item (unified task+event) lifecycle events — itemkit.
const (
	ItemScheduled            = sharedbus.ItemScheduled
	ItemUnscheduled          = sharedbus.ItemUnscheduled
	ItemRescheduled          = sharedbus.ItemRescheduled
	ItemRenamed              = sharedbus.ItemRenamed
	ItemDeleted              = sharedbus.ItemDeleted
	ItemReconciled           = sharedbus.ItemReconciled
	ItemActorAdded           = sharedbus.ItemActorAdded
	ItemActorRemoved         = sharedbus.ItemActorRemoved
	ItemVisibilityChanged    = sharedbus.ItemVisibilityChanged
	ItemMilestoneLinkAdded   = sharedbus.ItemMilestoneLinkAdded
	ItemMilestoneLinkRemoved = sharedbus.ItemMilestoneLinkRemoved
)

// Public share events — calendar_public_shares.
const (
	SharePublished     = sharedbus.SharePublished
	ShareUpdated       = sharedbus.ShareUpdated
	ShareTokenRotated  = sharedbus.ShareTokenRotated
	ShareDeleted       = sharedbus.ShareDeleted
	ShareEventAttached = sharedbus.ShareEventAttached
	ShareEventDetached = sharedbus.ShareEventDetached
)

// Legacy / compatibility kinds.
const (
	CommentAddedLegacy = sharedbus.CommentAddedLegacy
)
