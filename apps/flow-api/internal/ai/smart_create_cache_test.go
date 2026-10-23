package ai

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
)

// TestProposalCacheKeyFollowsVisibility holds the proposal cache to the
// set of tasks a proposal could have drawn on: the same title from an
// elevated caller and from a member must not share an entry, nor two
// members with different readable sets, while elevated callers — who all
// read every task — may.
func TestProposalCacheKeyFollowsVisibility(t *testing.T) {
	t.Parallel()

	key := func(vis acl.VisibilityArgs) string {
		return ProposalCacheKey(1, "smart_create", VisibilityCacheScope(vis), "Same title", "")
	}
	owner := acl.VisibilityArgs{IsElevated: 1, ActorUserID: 10}
	admin := acl.VisibilityArgs{IsElevated: 1, ActorUserID: 11}
	member := acl.VisibilityArgs{IsElevated: 0, ActorUserID: 12}
	other := acl.VisibilityArgs{IsElevated: 0, ActorUserID: 13}

	if key(owner) == key(member) {
		t.Error("a member must not be served a proposal cached for an elevated caller")
	}
	if key(member) == key(other) {
		t.Error("two members' readable sets differ; their proposals must not share an entry")
	}
	if key(owner) != key(admin) {
		t.Error("elevated callers read the same set and may share an entry")
	}
}

// TestProposeStepsCacheKeyFollowsVisibilityAndChildren holds the
// decomposition cache to every prompt input: the actor's readable set
// and the existing children the model is told not to duplicate.
func TestProposeStepsCacheKeyFollowsVisibilityAndChildren(t *testing.T) {
	t.Parallel()

	owner := acl.VisibilityArgs{IsElevated: 1, ActorUserID: 10}
	member := acl.VisibilityArgs{IsElevated: 0, ActorUserID: 12}
	one := []ChildTaskSummary{{Title: "Draft", State: "open"}}
	two := append([]ChildTaskSummary{}, one[0], ChildTaskSummary{Title: "Review", State: "open"})
	key := func(vis acl.VisibilityArgs, children []ChildTaskSummary) string {
		return proposeStepsCacheKey(1, "Ship it", "", GranularityStandard, children, vis)
	}

	if key(owner, one) == key(member, one) {
		t.Error("a member must not be served steps cached for an elevated caller")
	}
	if key(member, one) == key(member, two) {
		t.Error("adding a subtask must change the propose-steps cache key")
	}
	if key(member, one) == key(member, []ChildTaskSummary{{Title: "Draft", State: "done"}}) {
		t.Error("a child's state is in the prompt and must be in the key")
	}
	if key(member, one) != key(member, []ChildTaskSummary{{Title: "Draft", State: "open"}}) {
		t.Error("identical inputs must share an entry")
	}
}
