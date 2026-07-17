package constraint

import (
	"errors"
	"fmt"
	"time"
)

// Facts is the read-only world the evaluator sees for one task. All
// fields are populated by the caller before Evaluate is invoked, so
// the evaluator itself remains a pure function and trivially
// reproducible under replay.
type Facts struct {
	// Now is the wall-clock reference for time.* builtins. Replay
	// tools pass the event occurred_at so past evaluations stay
	// deterministic.
	Now time.Time
	// DueOn is the current task's due_on date (nil when unset).
	DueOn *time.Time
	// DependencyStates maps a referenced task public_id to its
	// current derived_state ("open" / "waiting" / "done" / ...). A
	// missing entry is treated as unresolved → false.
	DependencyStates map[string]string
	// DependencyKinds maps a referenced task public_id to its
	// dependency kind ("blocks" / "relates" / "duplicates" /
	// "subtask_of" / "retro_of"). dependency.open_at_most only
	// counts kinds that actually gate progress (see blockingDepKind);
	// informational links like "relates" / "retro_of" never block.
	// A public_id absent from this map is treated as blocking so that
	// callers which do not populate kinds keep counting every dep.
	DependencyKinds map[string]string
	// ActorRoles is the set of roles the acting user currently holds
	// on this task. The map is keyed by role name with true values;
	// absent keys are false.
	ActorRoles map[string]bool
	// SignalsReceived is the set of inbound signal kinds that have
	// fired on this task (e.g. "github.pr.merged").
	SignalsReceived map[string]bool
	// Approvals is the set of approval roles that have been granted.
	Approvals map[string]bool
	// CIStatus is the most recent CI outcome string
	// ("success" / "failure" / "pending" / ""). Empty means unknown.
	CIStatus string
}

// ErrEval is returned by Evaluate when a well-parsed constraint
// cannot be resolved against the provided facts (for example a
// malformed date literal). Unknown Ops never reach Evaluate because
// Parse rejects them first.
var ErrEval = errors.New("constraint: eval failed")

// Evaluate returns whether c is satisfied by f. It is a pure function
// of its inputs: same (Constraint, Facts) → same result, always.
func Evaluate(c Constraint, f Facts) (bool, error) {
	switch c.Op {
	case OpAnd:
		for _, t := range c.Terms {
			ok, err := Evaluate(t, f)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case OpOr:
		for _, t := range c.Terms {
			ok, err := Evaluate(t, f)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case OpNot:
		ok, err := Evaluate(*c.Term, f)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case OpTimeDueBefore:
		// true iff the task's due_on is strictly before c.Arg
		// interpreted as a calendar date in UTC.
		cutoff, err := time.Parse("2006-01-02", c.Arg)
		if err != nil {
			return false, fmt.Errorf("%w: %v", ErrEval, err)
		}
		if f.DueOn == nil {
			return false, nil
		}
		return f.DueOn.Before(cutoff), nil
	case OpTimeDueAfter:
		cutoff, err := time.Parse("2006-01-02", c.Arg)
		if err != nil {
			return false, fmt.Errorf("%w: %v", ErrEval, err)
		}
		if f.DueOn == nil {
			return false, nil
		}
		return f.DueOn.After(cutoff), nil
	case OpDepAllDone:
		for _, id := range c.TaskIDs {
			// A cancelled dependency is terminal: it will never
			// transition back to open, so it satisfies all_done just
			// like "done". Treating cancelled as unresolved would
			// permanently stall goal/sprint constraints.
			if !isTerminalState(f.DependencyStates[id]) {
				return false, nil
			}
		}
		return true, nil
	case OpDepOpenAtMost:
		if c.Max == nil {
			return false, fmt.Errorf("%w: dependency.open_at_most missing max threshold", ErrEval)
		}
		open := 0
		for id, st := range f.DependencyStates {
			if isTerminalState(st) {
				continue
			}
			// Only blocking-kind dependencies gate progress;
			// informational links (relates / retro_of / duplicates)
			// must not inflate the open count. A dep with no recorded
			// kind is treated as blocking for backward compatibility.
			if !blockingDepKind(f.DependencyKinds[id]) {
				continue
			}
			open++
		}
		return open <= *c.Max, nil
	case OpActorHasRole:
		return f.ActorRoles[c.Arg], nil
	case OpSignalReceived:
		return f.SignalsReceived[c.Arg], nil
	case OpApprovalGranted:
		return f.Approvals[c.Arg], nil
	case OpCIStatusIs:
		return f.CIStatus == c.Arg, nil
	}
	return false, fmt.Errorf("%w: unreachable op %q", ErrEval, c.Op)
}

// isTerminalState reports whether a dependency's derived_state is
// terminal — i.e. it will never transition back to an unresolved state.
// Both "done" and "cancelled" are terminal and count as resolved for
// dependency.all_done and dependency.open_at_most. Keeping this shared
// prevents the two operators from drifting on what "resolved" means.
func isTerminalState(state string) bool {
	switch state {
	case "done", "cancelled":
		return true
	default:
		return false
	}
}

// blockingDepKind reports whether a dependency kind actually gates the
// dependent task's progress. "blocks" and "subtask_of" hold work back;
// "relates", "duplicates", and "retro_of" are informational and must
// not count toward dependency.open_at_most. An empty kind is treated
// as blocking so callers that do not populate kinds keep the prior
// (count-everything) behaviour.
func blockingDepKind(kind string) bool {
	switch kind {
	case "relates", "duplicates", "retro_of":
		return false
	default:
		return true
	}
}
