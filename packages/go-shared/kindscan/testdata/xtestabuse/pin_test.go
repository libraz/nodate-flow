package xtestabuse_test

import (
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan/testdata/xtestabuse"
)

// pinned is the write the rule exists for, in the place it is easiest to
// write unnoticed: a test asserting on a spelling, in a package the
// compiler builds separately from the one it tests.
func pinned() eventbus.Kind {
	return "calendar.subscribed"
}

// viaHelper reaches a declaration that lives in an in-package test file,
// so this file only type-checks if the package under test is resolved as
// the test build assembles it.
func viaHelper() eventbus.Kind {
	return xtestabuse.PinnedKind()
}

// sanctioned is the same value written the way the rule asks for, and
// must not be reported.
func sanctioned() eventbus.Kind {
	return xtestabuse.Sanctioned()
}
