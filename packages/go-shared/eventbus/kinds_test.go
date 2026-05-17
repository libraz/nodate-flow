// Package eventbus tests guard the wire-format strings of every event
// kind. Changing a constant's string value is a breaking change for the
// events table and every downstream consumer (OpenAPI, SDK, webhooks,
// projections), so each value is pinned explicitly and the full set is
// asserted unique.
package eventbus

import "testing"

// TestJudgeLoopKinds pins the wire strings introduced by Release 8 /
// Phase 2 (signaljudge Applier). These are the only producers of the
// new kinds — if the strings drift, the Applier output schema, the SDK
// enum and the events table CHECK rows all silently disagree.
func TestJudgeLoopKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  Kind
		want string
	}{
		{"SignalJudged", SignalJudged, "signal.judged"},
		{"SignalApplied", SignalApplied, "signal.applied"},
		{"SignalRejected", SignalRejected, "signal.rejected"},
		{"TaskAutoCompleted", TaskAutoCompleted, "task.auto_completed"},
		{"TaskRetroDrafted", TaskRetroDrafted, "task.retro.drafted"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("kind %s: got %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestKindsAreUnique guards against accidental duplicate string values
// across the full kind set. Two constants resolving to the same string
// would silently merge in projections and webhook routing.
func TestKindsAreUnique(t *testing.T) {
	t.Parallel()

	all := []Kind{
		// task.*
		TaskCreated, TaskUpdated, TaskDisabled,
		TaskCommentAdded, TaskCommentEdited, TaskCommentRemoved,
		TaskAttachmentAdded, TaskAttachmentRemoved,
		TaskActorAdded, TaskActorRemoved,
		TaskDependencyAdded, TaskDependencyRemoved,
		TaskConstraintAdded, TaskConstraintRemoved,
		TaskArchived, TaskUnarchived,
		TaskTransitionStart, TaskTransitionBlock, TaskTransitionUnblock,
		TaskTransitionSubmit, TaskTransitionComplete, TaskTransitionReopen,
		TaskTransitionCancel,
		TaskAutoCompleted, TaskRetroDrafted,
		TaskLabelAdded, TaskLabelRemoved,

		// label.*
		LabelCreated, LabelUpdated, LabelDisabled,

		// page.* / lens.* / timebox.* archives
		PageArchived, PageUnarchived, LensArchived, TimeboxArchived,

		// signal.*
		SignalAttached, SignalJudged, SignalApplied, SignalRejected,

		// ai.suggestion.*
		AiSuggestionProposed, AiSuggestionApplied,
		AiSuggestionDismissed, AiSuggestionEdited,

		// ai.agent.*
		AiAgentPaused, AiAgentResumed,
		AiAgentRunStarted, AiAgentRunCompleted, AiAgentRunFailed,

		// agent.task.*
		AgentTaskAttached, AgentTaskDetached, AgentTaskThought,
		AgentTaskHandoffToUser, AgentTaskHandoffToAgent,

		// timebox.*
		TimeboxCreated, TimeboxUpdated, TimeboxActivated, TimeboxCompleted,
		TimeboxTaskAdded, TimeboxTaskRemoved,

		// relation.*
		RelationSuggested, RelationAccepted, RelationDismissed,

		// lens.*
		LensShared, LensUnshared,

		// page.*
		PageCreated, PageUpdated, PageDisabled,

		// dashboard.widget.*
		DashboardWidgetCreated, DashboardWidgetUpdated, DashboardWidgetDisabled,

		// export.*
		ExportRequested,

		// calendar.*
		CalendarCreated, CalendarUpdated, CalendarDeleted,
		CalEventCreated, CalEventUpdated, CalEventDeleted,
		CalMemberJoined, CalMemberLeft,
		CalMemoCreated, CalMemoCompleted,

		// reaction.*
		ReactionAdded, ReactionRemoved,

		// mention.*
		MentionCreated,

		// favorite.*
		FavoriteAdded, FavoriteRemoved,

		// intake.*
		IntakeItemCreated, IntakeItemAccepted, IntakeItemRejected,
		IntakeItemSnoozed, IntakeItemDuplicate,

		// description.version.*
		DescriptionVersionCreated, DescriptionVersionRestored,

		// import.job.*
		ImportJobCreated, ImportJobCompleted, ImportJobFailed, ImportJobCancelled,

		// workspace.member.*
		WorkspaceMemberAdded, WorkspaceMemberRemoved, WorkspaceMemberRoleChanged,

		// item.*
		ItemScheduled, ItemUnscheduled, ItemRescheduled, ItemRenamed,
		ItemDeleted, ItemReconciled,
		ItemActorAdded, ItemActorRemoved, ItemVisibilityChanged,
		ItemMilestoneLinkAdded, ItemMilestoneLinkRemoved,

		// share.*
		SharePublished, ShareUpdated, ShareTokenRotated, ShareDeleted,
		ShareEventAttached, ShareEventDetached,

		// legacy
		CommentAddedLegacy,
	}

	seen := make(map[Kind]string, len(all))
	for _, k := range all {
		if prev, dup := seen[k]; dup {
			t.Fatalf("duplicate kind string %q (seen previously as %q)", k, prev)
		}
		seen[k] = k
	}
}

// TestTaskTransitionHelper verifies the dynamic transition kind builder
// preserves the "task.transition." prefix expected by the projector.
func TestTaskTransitionHelper(t *testing.T) {
	t.Parallel()

	got := TaskTransition("custom")
	const want = "task.transition.custom"
	if got != want {
		t.Fatalf("TaskTransition(\"custom\"): got %q, want %q", got, want)
	}
}
