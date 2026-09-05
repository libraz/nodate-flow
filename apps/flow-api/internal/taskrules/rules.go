// Package taskrules holds the input preconditions a tasks write has to
// pass, in one place every transport that performs one can reach: the
// REST handlers under internal/http/handlers/tasks and the task tools in
// internal/mcp.
//
// The rules are small — a title is not blank, a due date is not earlier
// than a start date — and that is exactly why each transport ended up
// stating them for itself, as literals inside the handler that happened
// to need them. A write path that states neither still produces a valid
// row, so nothing signals the gap: a title of only spaces is stored as a
// title of only spaces, and a task shows up in a list with nothing to
// click on.
//
// Unlike [calendarrules], the functions here return plain sentinels
// rather than an *apierrors.APIError. The refusals are not one answer:
// the same blank title is a 422 naming the field on REST and a tool
// error on MCP, and both contracts have to keep holding. So the rule
// says what was violated and the transport says what that means to its
// caller, through [Classify] — the same split handlerutil already uses
// for itemkit errors, where one classifier feeds two translators.
package taskrules

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var (
	// ErrTitleEmpty reports a title that carries no visible characters.
	ErrTitleEmpty = errors.New("taskrules: title is empty")
	// ErrDueBeforeStart reports a due date earlier than the start date
	// it is paired with.
	ErrDueBeforeStart = errors.New("taskrules: due date precedes start date")
)

// Title is a title that has passed the rule. Its payload is unexported
// and there is no literal form, so the only way to hold one is
// [NewTitle] or [PatchTitle] — which is what makes the rule reach a
// write path rather than waiting to be called from it.
//
// That matters because the writers are not all visible to a check that
// walks the sql/queries statements. itemkit.RenameItem writes
// tasks.title through a raw ExecContext, so no query-derived guard can
// see it; requiring this type at the boundary is what reaches it. The
// same shape is already house style here — types.PublicID is an
// unexported payload with a String() that crosses into sqlc params.
//
// The zero value carries an empty string. It is not reachable through
// the constructors and the two write funnels refuse it, so it means
// "nobody filled this field in" rather than a title.
type Title struct{ s string }

// String returns the stored form: trimmed, non-empty, ready to bind.
func (t Title) String() string { return t.s }

// MarshalJSON renders the title as the JSON string it stands for.
//
// Without it the unexported payload encodes as {} — and the places a
// title crosses into JSON are event and audit payloads typed
// map[string]any, where nothing checks the value and the rows are
// append-only. A timeline entry that recorded {} as the new title
// cannot be corrected afterwards.
func (t Title) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.s)
}

// NewTitle normalises a submitted title and refuses a blank one.
//
// The trim is part of the rule, not a courtesy: the stored value is the
// trimmed one, so " " and "" have to be the same request, and a title
// that arrives padded is stored the way it reads.
func NewTitle(raw string) (Title, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Title{}, ErrTitleEmpty
	}
	return Title{s: trimmed}, nil
}

// PatchTitle resolves the title a partial update leaves behind.
//
// A nil pointer means the body did not mention the title, so the value
// already in the column stands and crosses verbatim: refusing it would
// make an unrelated update fail on a row written before the rule
// existed, and trimming it would rewrite a stored value the caller never
// mentioned. A present pointer is submitted input and goes through
// [NewTitle], so clearing a title by sending "" or "   " is refused
// rather than treated as "leave it alone".
//
// current is a plain string because it is read straight off a sqlc row.
// Taking a [Title] there would need an exported way to lift an arbitrary
// string into one, and that function is reachable from every call site
// that ought to be going through [NewTitle] — so the two constructors
// here are the whole surface, and both take raw input.
func PatchTitle(current string, in *string) (Title, error) {
	if in == nil {
		return Title{s: current}, nil
	}
	return NewTitle(*in)
}

// DateOrder refuses a task whose due date falls before its start date.
//
// Both values are the pair as it would be stored, which for a partial
// update means the request merged over the persisted row: a body that
// moves only one side still has to face the other side's stored value,
// or a task can be inverted one field at a time.
//
// Equal dates are allowed — a task started and due the same day is
// ordinary. A NULL on either side is unconstrained rather than a
// violation: a task with only a due date has no ordering to break.
//
// The comparison is between instants, not calendar days, so two values
// on one day are ordered by their clock time. That distinction does not
// arise today because the columns are dates and every caller parses to
// midnight, but it is what the code does and a caller holding a
// timestamp would meet it.
func DateOrder(due, started sql.NullTime) error {
	if !due.Valid || !started.Valid {
		return nil
	}
	if due.Time.Before(started.Time) {
		return ErrDueBeforeStart
	}
	return nil
}

// Violation is the classifier output. It stays narrow on purpose: every
// transport maps each value onto its own vocabulary, so a rule added
// here is added to this list first and then answered everywhere.
type Violation int

const (
	// ViolationNone is the sentinel for "no error".
	ViolationNone Violation = iota
	// ViolationTitleEmpty is [ErrTitleEmpty].
	ViolationTitleEmpty
	// ViolationDueBeforeStart is [ErrDueBeforeStart].
	ViolationDueBeforeStart
	// ViolationOther is anything that did not come from this package.
	// A transport maps it to its own internal-failure answer rather
	// than guessing at a field name.
	ViolationOther
)

// Classify names the rule an error violated. nil → [ViolationNone].
func Classify(err error) Violation {
	switch {
	case err == nil:
		return ViolationNone
	case errors.Is(err, ErrTitleEmpty):
		return ViolationTitleEmpty
	case errors.Is(err, ErrDueBeforeStart):
		return ViolationDueBeforeStart
	}
	return ViolationOther
}
