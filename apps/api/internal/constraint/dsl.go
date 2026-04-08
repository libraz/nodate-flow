// Package constraint implements the Phase 3 constraint DSL: a
// JSON-structured, side-effect-free expression language that any
// task_constraints row can embed in its `expression` column.
//
// The DSL is intentionally closed-form — no loops, no user-defined
// functions, no identifiers outside the builtins declared below — so
// that Evaluate stays a pure function of (Constraint, Facts). This is
// what makes replay equivalence (docs/plan/phase-3-constraint.md
// §3.TEST-3) possible: re-running events from scratch must always
// produce the same derived_state.
//
// The surface is deliberately tiny for the first slice; more builtins
// land in 3.DSL-3.
package constraint

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Op is the discriminator for every Constraint node. Keeping it a
// string (rather than an int const) makes JSON round-trips obvious in
// logs and mimics the shape the web editor (3.WEB-1) will emit.
type Op string

// Supported operations for the first Phase 3 slice. Any Op not in
// this set is rejected by Parse.
const (
	OpAnd           Op = "and"
	OpOr            Op = "or"
	OpNot           Op = "not"
	OpTimeDueBefore Op = "time.due_before"
	OpTimeDueAfter  Op = "time.due_after"
	OpDepAllDone    Op = "dependency.all_done"
	OpDepOpenAtMost Op = "dependency.open_at_most"
	OpActorHasRole  Op = "actor.has_role"
	OpSignalReceived Op = "signal.received"
	OpApprovalGranted Op = "approval.granted"
	OpCIStatusIs    Op = "ci.status_is"
)

// Constraint is the parsed DSL AST. It is a single struct with
// optional fields rather than an interface tree so it round-trips
// through JSON cleanly and so evaluation can switch on Op without
// type assertions.
type Constraint struct {
	Op Op `json:"op"`

	// Terms is used by "and" / "or".
	Terms []Constraint `json:"terms,omitempty"`
	// Term is used by "not".
	Term *Constraint `json:"term,omitempty"`

	// Arg is the generic string payload: ISO date for time.due_before,
	// role name for actor.has_role.
	Arg string `json:"arg,omitempty"`
	// TaskIDs is the id list for dependency.* builtins.
	TaskIDs []string `json:"taskIds,omitempty"`
	// Max is the integer threshold for count.* builtins
	// (e.g. dependency.open_at_most).
	Max *int `json:"max,omitempty"`
}

// ErrParse is returned by Parse when the input cannot be decoded or
// contains an unknown Op / missing required field. Callers should
// treat it as a validation error (AI.RESPONSE.PARSE_FAILED for the
// AI path, VALIDATION.BODY.FIELD.INVALID for the hand-edit path).
var ErrParse = errors.New("constraint: parse failed")

// Parse decodes a JSON-encoded constraint expression and validates
// that every referenced Op is known and every required field is
// present. It does NOT evaluate the constraint.
func Parse(raw []byte) (Constraint, error) {
	var c Constraint
	if err := json.Unmarshal(raw, &c); err != nil {
		return Constraint{}, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if err := validate(&c); err != nil {
		return Constraint{}, err
	}
	return c, nil
}

// validate walks the AST and enforces per-Op invariants.
func validate(c *Constraint) error {
	switch c.Op {
	case OpAnd, OpOr:
		if len(c.Terms) == 0 {
			return fmt.Errorf("%w: %s requires non-empty terms", ErrParse, c.Op)
		}
		for i := range c.Terms {
			if err := validate(&c.Terms[i]); err != nil {
				return err
			}
		}
	case OpNot:
		if c.Term == nil {
			return fmt.Errorf("%w: not requires term", ErrParse)
		}
		return validate(c.Term)
	case OpTimeDueBefore, OpTimeDueAfter:
		if c.Arg == "" {
			return fmt.Errorf("%w: %s requires arg (YYYY-MM-DD)", ErrParse, c.Op)
		}
	case OpDepAllDone:
		if len(c.TaskIDs) == 0 {
			return fmt.Errorf("%w: dependency.all_done requires taskIds", ErrParse)
		}
	case OpDepOpenAtMost:
		if c.Max == nil || *c.Max < 0 {
			return fmt.Errorf("%w: dependency.open_at_most requires non-negative max", ErrParse)
		}
	case OpActorHasRole, OpSignalReceived, OpApprovalGranted, OpCIStatusIs:
		if c.Arg == "" {
			return fmt.Errorf("%w: %s requires arg", ErrParse, c.Op)
		}
	default:
		return fmt.Errorf("%w: unknown op %q", ErrParse, c.Op)
	}
	return nil
}
