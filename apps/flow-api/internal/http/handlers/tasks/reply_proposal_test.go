package tasks

import (
	"testing"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

func TestDeterministicReplyProposal(t *testing.T) {
	tests := []struct {
		name        string
		reply       string
		state       generated.TasksDerivedState
		wantHandled bool
		wantDue     string
		wantAction  string
		wantAsk     bool
	}{
		{name: "exact completion", reply: "complete", state: generated.TasksDerivedStateOpen, wantHandled: true, wantAction: taskstate.TransitionComplete},
		{name: "Japanese completion", reply: "完了", state: generated.TasksDerivedStateOpen, wantHandled: true, wantAction: taskstate.TransitionComplete},
		{name: "explicit Japanese completion", reply: "タスクを完了にしてください。", state: generated.TasksDerivedStateOpen, wantHandled: true, wantAction: taskstate.TransitionComplete},
		{name: "completion is state checked", reply: "complete", state: generated.TasksDerivedStateDone, wantHandled: true, wantAsk: true},
		{name: "date command", reply: "due 2035-04-05", state: generated.TasksDerivedStateOpen, wantHandled: true, wantDue: "2035-04-05"},
		{name: "invalid calendar date", reply: "due 2035-02-30", state: generated.TasksDerivedStateOpen, wantHandled: true, wantAsk: true},
		{name: "missing date", reply: "due", state: generated.TasksDerivedStateOpen, wantHandled: true, wantAsk: true},
		{name: "freeform remains for model", reply: "返信を記録してください", state: generated.TasksDerivedStateOpen, wantHandled: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, handled := deterministicReplyProposal(tc.reply, tc.state)
			if handled != tc.wantHandled {
				t.Fatalf("handled = %v, want %v", handled, tc.wantHandled)
			}
			if got.Transition != tc.wantAction {
				t.Errorf("transition = %q, want %q", got.Transition, tc.wantAction)
			}
			if got.DueOn == nil && tc.wantDue != "" {
				t.Fatalf("dueOn = nil, want %q", tc.wantDue)
			}
			if got.DueOn != nil && *got.DueOn != tc.wantDue {
				t.Errorf("dueOn = %q, want %q", *got.DueOn, tc.wantDue)
			}
			if (got.Clarification != "") != tc.wantAsk {
				t.Errorf("clarification = %q, want present=%v", got.Clarification, tc.wantAsk)
			}
		})
	}
}

func TestSanitizeReplyProposalUsesCurrentStateAndExplicitCompletionIntent(t *testing.T) {
	complete := modelReplyProposal{Transition: taskstate.TransitionComplete}
	got := sanitizeReplyProposal(complete, generated.TasksDerivedStateOpen, false)
	if got.Transition != "" || got.Clarification == "" {
		t.Fatalf("text-only response must not propose completion: %#v", got)
	}

	got = sanitizeReplyProposal(complete, generated.TasksDerivedStateOpen, true)
	if got.Transition != taskstate.TransitionComplete || got.Clarification != "" {
		t.Fatalf("explicit completion intent should propose a legal transition: %#v", got)
	}

	got = sanitizeReplyProposal(modelReplyProposal{Transition: taskstate.TransitionReopen}, generated.TasksDerivedStateOpen, true)
	if got.Transition != "" || got.Clarification == "" {
		t.Fatalf("illegal transition should become a clarification: %#v", got)
	}
}

func TestReplyClearlyRequestsCompletionRejectsNegation(t *testing.T) {
	for _, reply := range []string{"完了にしてはいけません", "do not close the task", "返信を記録してください"} {
		if replyClearlyRequestsCompletion(reply) {
			t.Errorf("%q must not authorize a completion proposal", reply)
		}
	}
	for _, reply := range []string{"完了にしてください。", "close the task!"} {
		if !replyClearlyRequestsCompletion(reply) {
			t.Errorf("%q should authorize a completion proposal", reply)
		}
	}
}

func TestReplyProposalCacheScopeIncludesActorAndTaskSnapshot(t *testing.T) {
	base := replyProposalSnapshot{
		publicID: uuid.New(),
		title:    "Check rollout",
		state:    generated.TasksDerivedStateOpen,
		dueOn:    "",
	}
	key := replyProposalCacheScope(3, base, "Asia/Tokyo", "2035-04-05")
	if key == replyProposalCacheScope(4, base, "Asia/Tokyo", "2035-04-05") {
		t.Fatal("changing actor must change the proposal scope")
	}
	changed := base
	changed.state = generated.TasksDerivedStateWaiting
	if key == replyProposalCacheScope(3, changed, "Asia/Tokyo", "2035-04-05") {
		t.Fatal("changing task state must change the proposal scope")
	}
	if key == replyProposalCacheScope(3, base, "Asia/Tokyo", "2035-04-06") {
		t.Fatal("changing local today must change the proposal scope")
	}
}
