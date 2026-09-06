package handlerutil

import "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"

// IsTaskDerivedState reports whether s names a value of the
// tasks.derived_state ENUM.
//
// The values are the sqlc-generated constants rather than string
// literals, so the schema stays the one place the enum is spelled: a
// value renamed or removed there stops this switch compiling instead of
// leaving a stale copy behind that silently accepts or refuses the wrong
// word.
//
// Handlers gate a caller-supplied state through here so the surfaces
// that read the same column agree on which states exist. They disagree
// the moment each keeps its own list, and on a public share page that
// disagreement decides which tasks a link holder is shown.
func IsTaskDerivedState(s string) bool {
	switch generated.TasksDerivedState(s) {
	case generated.TasksDerivedStateOpen,
		generated.TasksDerivedStateWaiting,
		generated.TasksDerivedStateReview,
		generated.TasksDerivedStateDone,
		generated.TasksDerivedStateCancelled:
		return true
	default:
		return false
	}
}
