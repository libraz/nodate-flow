// Package undeclaredabuse is input for kindscan's own tests, not part of
// the build: it holds the three cases a scan has to tell apart, and the
// only way to be sure it does is to hand it code that contains them.
//
// Nothing here is ever called. It lives under testdata so the go tool
// ignores it, which is also why the two rejected forms can sit in the
// tree at all — a package the guard walked would fail on them, correctly.
package undeclaredabuse

import (
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// LaunderedThroughEscape is the case the escape must not permit: a
// declared kind, routed through Undeclared so the argument is typed
// string and the literal rule cannot see it.
func LaunderedThroughEscape() eventbus.Kind {
	return kindscan.Undeclared("task.created")
}

// BareLiteral is the original case, unchanged by the escape existing.
func BareLiteral() eventbus.Kind {
	return "task.updated"
}

// GenuinelyUndeclared is what the escape is for, and must be accepted.
func GenuinelyUndeclared() eventbus.Kind {
	return kindscan.Undeclared("nothing.declares.this")
}
