package taskservice_test

// Effective-mode model for one set_priority step (pairwise, strength 2).
//
//	grant:     auto | approval | none
//	delegator: capable (owner) | demoted (project commenter) | none
//	trigger:   same as delegator | capable (project editor) | incapable (project commenter)
//
// Rows are an L9 orthogonal array over the three factors, which covers
// every pair of levels once. Two rows are added so that each line of the
// auto branch of the mode table is hit directly: (auto, capable, capable)
// and (auto, capable, incapable). "same" with no delegator means the run
// has a capable trigger user but no delegator.
//
// Expected: none -> not_permitted; approval -> proposed; auto -> applied
// only when both the delegator and the trigger user hold the capability,
// otherwise proposed.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

type modeRow struct {
	grant, delegator, trigger string
}

var modeRows = []modeRow{
	{"auto", "capable", "same"},
	{"auto", "demoted", "capable"},
	{"auto", "none", "incapable"},
	{"approval", "capable", "capable"},
	{"approval", "demoted", "incapable"},
	{"approval", "none", "same"},
	{"none", "capable", "incapable"},
	{"none", "demoted", "same"},
	{"none", "none", "capable"},
	{"auto", "capable", "capable"},
	{"auto", "capable", "incapable"},
}

func expectedMode(r modeRow) taskservice.StepOutcome {
	switch r.grant {
	case "none":
		return taskservice.StepNotPermitted
	case "approval":
		return taskservice.StepProposed
	}
	delegatorOK := r.delegator == "capable"
	triggerOK := r.trigger == "capable" || (r.trigger == "same" && delegatorOK) || (r.trigger == "same" && r.delegator == "none")
	if delegatorOK && triggerOK {
		return taskservice.StepApplied
	}
	return taskservice.StepProposed
}

func TestExecute_EffectiveMode(t *testing.T) {
	for _, r := range modeRows {
		t.Run(fmt.Sprintf("%s/%s/%s", r.grant, r.delegator, r.trigger), func(t *testing.T) {
			k := newAgentKit(t)
			switch r.grant {
			case "none":
				k.grant(map[string]string{execution.ToolAddComment: "auto"})
			default:
				k.grantAll(r.grant)
			}
			editor, _ := k.addUser("editor")
			commenter, _ := k.addUser("commenter")

			var delegator uint32
			switch r.delegator {
			case "capable":
				delegator = k.f.userID
			case "demoted":
				delegator = commenter
			}
			k.delegate(delegator)
			var trigger uint32
			switch r.trigger {
			case "same":
				trigger = delegator
				if delegator == 0 {
					trigger = editor
				}
			case "capable":
				trigger = editor
			case "incapable":
				trigger = commenter
			}

			want := expectedMode(r)
			out := k.doAs(k.run(delegator, trigger), priorityAction(3))
			requireOutcome(t, want, out)
			wantPriority := int32(1)
			if want == taskservice.StepApplied {
				wantPriority = 3
			}
			require.Equal(t, wantPriority, k.task().priority)
			wantProposals := 0
			if want == taskservice.StepProposed {
				wantProposals = 1
			}
			require.Equal(t, wantProposals, k.proposals("pending"))
		})
	}
}
