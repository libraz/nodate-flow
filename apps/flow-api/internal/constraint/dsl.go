// Package constraint implements the constraint DSL: a
// JSON-structured, side-effect-free expression language that any
// task_constraints row can embed in its `expression` column.
//
// The DSL is intentionally closed-form — no loops, no user-defined
// functions, no identifiers outside the builtins declared below — so
// that Evaluate stays a pure function of (Constraint, Facts). This is
// what makes replay equivalence possible: re-running events from
// scratch must always
// produce the same derived_state.
//
// The surface is deliberately tiny for the first slice; more builtins
// land in a future release.
package constraint

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Op is the discriminator for every Constraint node. Keeping it a
// string (rather than an int const) makes JSON round-trips obvious in
// logs and mimics the shape the web editor will emit.
type Op string

// Supported operations for the initial constraint slice. Any Op not in
// this set is rejected by Parse.
const (
	OpAnd             Op = "and"
	OpOr              Op = "or"
	OpNot             Op = "not"
	OpTimeDueBefore   Op = "time.due_before"
	OpTimeDueAfter    Op = "time.due_after"
	OpDepAllDone      Op = "dependency.all_done"
	OpDepOpenAtMost   Op = "dependency.open_at_most"
	OpActorHasRole    Op = "actor.has_role"
	OpSignalReceived  Op = "signal.received"
	OpApprovalGranted Op = "approval.granted"
	OpCIStatusIs      Op = "ci.status_is"
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

// ErrParse is the sentinel returned by Parse. Callers compare with
// errors.Is; the concrete error returned by Parse is always a
// *ParseError which carries a stable i18n Code.
var ErrParse = errors.New("constraint: parse failed")

// ParseError is the structured error produced by Parse. The Code
// field is one of the CONSTRAINT.PARSE.* stable codes declared in
// errors/constraint.yaml and is suitable for direct use as an i18n
// message key.
type ParseError struct {
	Code    string
	Message string
}

// Error implements the error interface.
func (e *ParseError) Error() string { return e.Code + ": " + e.Message }

// Is reports whether target is ErrParse so callers can use
// errors.Is(err, ErrParse) without caring about the concrete type.
func (e *ParseError) Is(target error) bool { return target == ErrParse }

func parseErr(code, format string, args ...any) *ParseError {
	return &ParseError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Parse decodes a JSON-encoded constraint expression and validates
// that every referenced Op is known and every required field is
// present. It does NOT evaluate the constraint.
func Parse(raw []byte) (Constraint, error) {
	var c Constraint
	if err := json.Unmarshal(raw, &c); err != nil {
		return Constraint{}, parseErr(CodeInvalidJSON, "invalid json: %v", err)
	}
	if err := validate(&c); err != nil {
		return Constraint{}, err
	}
	return c, nil
}

// Stable i18n codes for parse errors. Keep in sync with
// errors/constraint.yaml.
const (
	CodeInvalidJSON         = "CONSTRAINT.PARSE.INVALID_JSON"
	CodeUnsupportedOperator = "CONSTRAINT.PARSE.UNSUPPORTED_OPERATOR"
	CodeMissingArg          = "CONSTRAINT.PARSE.MISSING_ARG"
	CodeEmptyTerms          = "CONSTRAINT.PARSE.EMPTY_TERMS"
)

// validate walks the AST and enforces per-Op invariants.
func validate(c *Constraint) error {
	switch c.Op {
	case OpAnd, OpOr:
		if len(c.Terms) == 0 {
			return parseErr(CodeEmptyTerms, "%s requires non-empty terms", c.Op)
		}
		for i := range c.Terms {
			if err := validate(&c.Terms[i]); err != nil {
				return err
			}
		}
	case OpNot:
		if c.Term == nil {
			return parseErr(CodeMissingArg, "not requires term")
		}
		return validate(c.Term)
	case OpTimeDueBefore, OpTimeDueAfter:
		if c.Arg == "" {
			return parseErr(CodeMissingArg, "%s requires arg (YYYY-MM-DD)", c.Op)
		}
	case OpDepAllDone:
		if len(c.TaskIDs) == 0 {
			return parseErr(CodeMissingArg, "dependency.all_done requires taskIds")
		}
	case OpDepOpenAtMost:
		if c.Max == nil || *c.Max < 0 {
			return parseErr(CodeMissingArg, "dependency.open_at_most requires non-negative max")
		}
	case OpActorHasRole, OpSignalReceived, OpApprovalGranted, OpCIStatusIs:
		if c.Arg == "" {
			return parseErr(CodeMissingArg, "%s requires arg", c.Op)
		}
	default:
		return parseErr(CodeUnsupportedOperator, "unknown op %q", c.Op)
	}
	return nil
}
