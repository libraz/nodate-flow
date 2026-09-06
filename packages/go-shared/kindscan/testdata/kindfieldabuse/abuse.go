// Package kindfieldabuse is input for kindscan's own tests, not part of
// the build: it holds the forms the origin rule has to tell apart at the
// field where an event kind stops being one.
//
// The struct stands in for the generated params struct. Testdata cannot
// import the real one — the go tool ignores this directory, so nothing
// here is reachable from a module graph — and the rule does not depend on
// which struct it is: what matters is a string field a kind is written
// to. The test names this package's spelling of it in its config.
package kindfieldabuse

import (
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// AppendEventParams stands in for the generated struct that carries an
// event into the row.
type AppendEventParams struct {
	Type        string
	WorkspaceID uint32
}

// FromLiteral is the case that shipped: the kind's name written out where
// nothing resolves it to a Kind, so the literal rule has nothing to see.
func FromLiteral() AppendEventParams {
	return AppendEventParams{Type: "calendar.reminder"}
}

// localType is the same case one indirection away — an untyped constant
// beside the writer, which is how the name stays out of the registry
// while reading like a declaration.
const localType = "calendar.reminder"

// FromLocalConst writes that constant.
func FromLocalConst() AppendEventParams {
	return AppendEventParams{Type: localType}
}

// FromAssignment fills the field in after the struct is declared, which
// the composite pass alone would not see.
func FromAssignment() AppendEventParams {
	var params AppendEventParams
	params.Type = "calendar.reminder"
	return params
}

// FromUnkeyedLiteral names no field, so the field is decided by
// position. Still a write, and still has to answer.
func FromUnkeyedLiteral() AppendEventParams {
	return AppendEventParams{"calendar.reminder", 1}
}

// FromKind is the accepted form: the value was a kind immediately before
// it became a string.
func FromKind() AppendEventParams {
	return AppendEventParams{Type: string(eventbus.CalendarReminder)}
}

// FromKindVariable is the same, through a value the caller supplies —
// how eventbus.Append writes every event there is.
func FromKindVariable(kind eventbus.Kind) AppendEventParams {
	return AppendEventParams{Type: string(kind)}
}

// FromKindAssignment is the accepted form of the assignment case.
func FromKindAssignment(kind eventbus.Kind) AppendEventParams {
	var params AppendEventParams
	params.Type = string(kind)
	return params
}

// FromUndeclaredEscape passes through the escape, which is what a test
// needs to write a kind no constant covers. A declared kind cannot be
// laundered this way: the escape refuses it, and the scan reports the
// call — that check is what keeps this route from reopening the rule.
func FromUndeclaredEscape() AppendEventParams {
	return AppendEventParams{Type: string(kindscan.Undeclared("nothing.declares.this"))}
}
