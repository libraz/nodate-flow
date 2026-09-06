package xtestabuse

import "github.com/libraz/nodate-flow/packages/go-shared/eventbus"

// PinnedKind is declared in an in-package test file and used from the
// external test package beside it, which is the ordinary way a test
// helper is shared between the two.
//
// Export data holds no such declaration, so a scan that resolves the
// package under test through export data alone cannot type-check the
// external package at all — and a package that will not check is a
// package the rule does not cover.
func PinnedKind() eventbus.Kind {
	return eventbus.TaskUpdated
}
