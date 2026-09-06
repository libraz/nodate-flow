// Package xtestabuse is input for kindscan's own tests, not part of the
// build. It is the package half of a directory whose external test
// package writes a kind as a literal — the file the scan has to reach.
package xtestabuse

import "github.com/libraz/nodate-flow/packages/go-shared/eventbus"

// Sanctioned writes the kind the way the rule asks for, so the fixture
// answers for both sides of the rule rather than only the failing one.
func Sanctioned() eventbus.Kind {
	return eventbus.TaskCreated
}
