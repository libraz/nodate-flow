// Package p must fail to compile, and must fail for a reason that has
// nothing to do with the commit boundary.
//
// It is the control for the gate's error matching. A check that treats
// any non-zero exit as "correctly refused" reads a syntax error, a
// renamed symbol or a broken import as proof of a property it never
// tested — and would stay green against a tree entirely in breach, where
// the refused programs compile fine and something else is what failed.
// The gate therefore matches the diagnostic, and this file proves the
// matching is real by failing the other way.
//
// This directory is testdata: the go tool never walks into it, so the
// file is compiled only by the gate in the parent package.
package p

import "context"

func referencesSomethingUndefined(ctx context.Context) error {
	return noSuchFunctionExistsAnywhere(ctx)
}
