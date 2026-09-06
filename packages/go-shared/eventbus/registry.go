package eventbus

import (
	"slices"
	"strings"
)

// declaredKinds is every kind constant this package declares, in
// declaration order.
//
// The list exists because Go constants are not enumerable at runtime and
// the consumers that must handle *all* of them — the notification
// fan-out's title table, any future projection — otherwise have nothing
// to iterate and no way to be told they are missing one. A static test
// in this package reads the constant declarations and fails when the two
// disagree, so the list cannot quietly fall behind.
//
// Kinds minted at runtime by [TaskTransition] are not here; they are
// covered by their family instead.
var declaredKinds = []Kind{
	TaskCreated, TaskUpdated, TaskDisabled,
	TaskCommentAdded, TaskCommentEdited, TaskCommentRemoved,
	TaskAttachmentAdded, TaskAttachmentRemoved,
	TaskActorAdded, TaskActorRemoved,
	TaskDependencyAdded, TaskDependencyRemoved,
	TaskConstraintAdded, TaskConstraintRemoved,

	LabelCreated, LabelUpdated, LabelDisabled,
	TaskLabelAdded, TaskLabelRemoved,

	TaskArchived, TaskUnarchived,
	PageArchived, PageUnarchived,
	LensArchived, TimeboxArchived,

	TaskTransitionStart, TaskTransitionBlock, TaskTransitionUnblock,
	TaskTransitionSubmit, TaskTransitionComplete, TaskTransitionReopen,
	TaskTransitionCancel,

	TaskAutoCompleted, TaskRetroDrafted,

	SignalAttached, SignalJudged, SignalApplied, SignalRejected,
	SignalArchived, SignalSnoozed,

	AiSuggestionProposed, AiSuggestionApplied,
	AiSuggestionDismissed, AiSuggestionEdited,

	AiAutoActionProposed,

	AiAgentPaused, AiAgentResumed,
	AiAgentRunStarted, AiAgentRunCompleted, AiAgentRunFailed,

	AgentTaskAttached, AgentTaskDetached, AgentTaskThought,
	AgentTaskHandoffToUser, AgentTaskHandoffToAgent,

	TimeboxCreated, TimeboxUpdated, TimeboxActivated, TimeboxCompleted,
	TimeboxTaskAdded, TimeboxTaskRemoved,

	RelationSuggested, RelationAccepted, RelationDismissed,

	LensCreated, LensUpdated, LensShared, LensUnshared,

	PageCreated, PageUpdated, PageDisabled,

	DashboardWidgetCreated, DashboardWidgetUpdated, DashboardWidgetDisabled,

	ExportRequested,

	CalendarCreated, CalendarUpdated, CalendarDeleted,
	CalendarSubscribed, CalendarSubscriptionUpdated,
	CalEventCreated, CalEventUpdated, CalEventDeleted,
	CalMemberAdded, CalMemberRemoved, CalMemberRoleChanged,
	CalMemoCreated, CalMemoUpdated, CalMemoCompleted, CalMemoDeleted,
	CalendarReminder,

	CalEventCommentCreated, CalEventCommentUpdated, CalEventCommentDeleted,
	CalEventAttachmentCreated, CalEventAttachmentDeleted,
	CalEventChecklistCreated, CalEventChecklistUpdated, CalEventChecklistDeleted,
	CalEventAttendeeAdded, CalEventAttendeeRemoved, CalEventRsvpUpdated,
	CalEventInviteCreated, CalEventInviteRotated, CalEventInviteRevoked,

	ReactionAdded, ReactionRemoved,

	MentionCreated,

	FavoriteAdded, FavoriteRemoved,

	IntakeItemCreated, IntakeItemAccepted, IntakeItemRejected,
	IntakeItemSnoozed, IntakeItemDuplicate,

	DescriptionVersionCreated, DescriptionVersionRestored,

	ImportJobCreated, ImportJobCompleted, ImportJobFailed, ImportJobCancelled,

	WorkspaceMemberAdded, WorkspaceMemberRemoved, WorkspaceMemberRoleChanged,

	ItemScheduled, ItemUnscheduled, ItemRescheduled, ItemRenamed,
	ItemDeleted, ItemReconciled,
	ItemActorAdded, ItemActorRemoved, ItemVisibilityChanged,
	ItemMilestoneLinkAdded, ItemMilestoneLinkRemoved,

	PublicShareCreated, PublicShareUpdated, PublicShareRotated,
	PublicShareDeleted, PublicShareEventsAttached,
	PublicShareEventsReordered, PublicShareEventDetached,

	CommentAddedLegacy,
}

// Kinds returns every declared event kind, sorted, so a consumer that
// must cover all of them can iterate rather than restate the list.
func Kinds() []Kind {
	out := make([]Kind, len(declaredKinds))
	copy(out, declaredKinds)
	slices.Sort(out)
	return out
}

// Family is the coarse namespace an event kind belongs to. It exists so
// the consumers that route on "what sort of thing changed" — the SSE
// stream, the notification fan-out — share one table instead of each
// carrying its own switch over dotted prefixes.
//
// A switch is the wrong shape for that question: it answers "" for
// anything it does not recognise, so a kind added later reaches the
// consumer, matches nothing, and is dropped without a word. The family
// table is total over the declared kinds instead — a constant that no
// family covers fails [FamilyOf], and the totality test in this package
// refuses it before it can be emitted.
type Family string

// The families every declared kind resolves to.
const (
	FamilyTask            Family = "task"
	FamilyLabel           Family = "label"
	FamilyAgentTask       Family = "agent.task"
	FamilyAiSuggestion    Family = "ai.suggestion"
	FamilyAiAutoAction    Family = "ai.auto_action"
	FamilyAiAgent         Family = "ai.agent"
	FamilySignal          Family = "signal"
	FamilyTimebox         Family = "timebox"
	FamilyRelation        Family = "relation"
	FamilyLens            Family = "lens"
	FamilyPage            Family = "page"
	FamilyDashboard       Family = "dashboard"
	FamilyExport          Family = "export"
	FamilyCalendar        Family = "calendar"
	FamilyPublicShare     Family = "public_share"
	FamilyItem            Family = "item"
	FamilyReaction        Family = "reaction"
	FamilyMention         Family = "mention"
	FamilyFavorite        Family = "favorite"
	FamilyIntake          Family = "intake"
	FamilyDescription     Family = "description"
	FamilyImport          Family = "import"
	FamilyWorkspaceMember Family = "workspace.member"
	FamilyComment         Family = "comment"
)

// familyPrefix maps each family onto the dotted prefix its kinds carry.
//
// Resolution is by prefix rather than by exact kind because
// [TaskTransition] mints `task.transition.<name>` at runtime from a
// free-form transition name: those kinds are real, they reach the same
// consumers, and no enumeration of constants can contain them.
//
// Matching takes the longest prefix so a nested namespace wins over its
// parent — ai.suggestion.* and ai.agent.* are separate families and
// there is no plain `ai.` family for them to fall into.
var familyPrefix = map[Family]string{
	FamilyTask:            "task.",
	FamilyLabel:           "label.",
	FamilyAgentTask:       "agent.task.",
	FamilyAiSuggestion:    "ai.suggestion.",
	FamilyAiAutoAction:    "ai.auto_action.",
	FamilyAiAgent:         "ai.agent.",
	FamilySignal:          "signal.",
	FamilyTimebox:         "timebox.",
	FamilyRelation:        "relation.",
	FamilyLens:            "lens.",
	FamilyPage:            "page.",
	FamilyDashboard:       "dashboard.",
	FamilyExport:          "export.",
	FamilyCalendar:        "calendar.",
	FamilyPublicShare:     "public_share.",
	FamilyItem:            "item.",
	FamilyReaction:        "reaction.",
	FamilyMention:         "mention.",
	FamilyFavorite:        "favorite.",
	FamilyIntake:          "intake.",
	FamilyDescription:     "description.",
	FamilyImport:          "import.",
	FamilyWorkspaceMember: "workspace.member.",
	FamilyComment:         "comment.",
}

// Families returns every family, sorted by name, so a consumer can build
// a table keyed by family and be told at test time when it has missed
// one.
func Families() []Family {
	out := make([]Family, 0, len(familyPrefix))
	for f := range familyPrefix {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// Prefix returns the dotted prefix shared by every kind in f, or "" when
// f is not a declared family.
func (f Family) Prefix() string { return familyPrefix[f] }

// FamilyOf reports which family an event kind belongs to. The second
// result is false when no family covers it, which is the state a caller
// must treat as a defect rather than as "nothing to do": every declared
// constant resolves, so a miss means the value did not come from one.
func FamilyOf(k Kind) (Family, bool) {
	return FamilyForEventType(string(k))
}

// FamilyForEventType is [FamilyOf] for a value that has already left Go
// — the `type` column, an SSE tap argument, a webhook payload — where
// the kind is carried as a plain string.
func FamilyForEventType(eventType string) (Family, bool) {
	var (
		best    Family
		bestLen int
		found   bool
	)
	for family, prefix := range familyPrefix {
		if !strings.HasPrefix(eventType, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			best, bestLen, found = family, len(prefix), true
		}
	}
	return best, found
}

// judgeOnlyKinds are the kinds only the signaljudge Applier may append
// (ADR 0008 D4). The set lives beside the constants rather than in the
// package that enforces it, because the enforcement is a runtime guard:
// a kind added to the judge loop and forgotten here is appendable by
// anyone, and nothing about the guard's own file would have said so.
//
// SignalAttached is deliberately absent — the public signal ingestion
// endpoint appends it before any judge run exists.
var judgeOnlyKinds = map[Kind]bool{
	TaskAutoCompleted: true,
	TaskRetroDrafted:  true,
	SignalJudged:      true,
	SignalApplied:     true,
	SignalRejected:    true,
}

// IsJudgeOnly reports whether k may only be appended by the signaljudge
// Applier.
func IsJudgeOnly(k Kind) bool { return judgeOnlyKinds[k] }

// JudgeOnlyKinds returns the reserved kinds, sorted, so callers can
// assert over the set without restating it.
func JudgeOnlyKinds() []Kind {
	out := make([]Kind, 0, len(judgeOnlyKinds))
	for k := range judgeOnlyKinds {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
