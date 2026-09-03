package router

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
)

// projectPrefixFloors is the role each operation under /projects/{prjId} is
// registered behind.
//
// Membership used to be the whole check on this prefix, which made every
// mutation available to a project viewer: renaming the project, soft-deleting
// it along with its tasks, self-promotion to lead, and removing the leads who
// granted the access in the first place. Splitting the prefix into three chi
// groups fixed that, and this table is what stops an operation drifting back
// into the read group — a change of floor is a change of who may call it, and
// it should not be possible to make one without editing this list.
var projectPrefixFloors = map[string]auth.Floor{
	// Membership is the whole check a read needs.
	"projects-get":               auth.FloorNone,
	"projects-dependencies-list": auth.FloorNone,
	"projects-members-list":      auth.FloorNone,

	// Reshaping the project the caller already works in.
	"projects-patch":   auth.FloorProjectEditor,
	"projects-disable": auth.FloorProjectEditor,

	// Deciding who reaches the project, held one role higher than editing
	// it: an editor who could also grant roles could promote themselves and
	// remove every lead above them.
	"projects-members-add":    auth.FloorProjectLead,
	"projects-members-remove": auth.FloorProjectLead,
}

// TestProjectPrefixOperationsCarryTheirDeclaredFloor resolves the table above
// against the floors the router actually records, and fails on an operation
// under the prefix that the table does not mention.
func TestProjectPrefixOperationsCarryTheirDeclaredFloor(t *testing.T) {
	t.Parallel()

	res := BuildResult(stubDeps(t))
	seen := map[string]bool{}

	for _, op := range res.AuthenticatedOps {
		want, declared := projectPrefixFloors[op.OperationID]
		if !declared {
			continue
		}
		seen[op.OperationID] = true
		if op.WriteFloor != want {
			t.Errorf("%s %s (%s) is registered behind the %q floor, want %q",
				op.Method, op.Path, op.OperationID, op.WriteFloor, want)
		}
	}

	for id := range projectPrefixFloors {
		if !seen[id] {
			t.Errorf("projectPrefixFloors names %q, which the router no longer registers — the entry describes a route that does not exist", id)
		}
	}
}
