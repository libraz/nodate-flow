package dbretry

import (
	"testing"

	shared "github.com/libraz/nodate-flow/packages/go-shared/dbretry"
)

// TestAliasesAreTheSharedTypes is the point of this package now that the
// implementation lives in packages/go-shared/dbretry: what it exports
// must BE the shared types, not look-alikes.
//
// Re-declaring them here instead of aliasing would compile and pass
// every local test while silently splitting the fan-out in two —
// flow-api's eventbus deferring to one commit boundary, the
// cross-service eventlog to another — and whichever appender lost the
// coin toss would deliver nothing. The behaviour of the boundary itself
// is covered in the shared package alongside the code.
func TestAliasesAreTheSharedTypes(t *testing.T) {
	t.Parallel()

	// Compiles only while Tx is the shared type rather than one declared
	// here; the interface assertions then follow from it.
	sameTx := func(tx *Tx) *shared.Tx { return tx }
	var _ shared.CommitBoundary = sameTx(nil)
	var _ shared.CommitBoundary = AutoCommit(nil)

	// The auto-commit path has no boundary to wait for, so a hook
	// registered through the alias runs at once.
	fired := 0
	AutoCommit(nil).AfterCommit(func() { fired++ })
	if fired != 1 {
		t.Fatalf("a hook with no enclosing transaction must fire immediately, fired = %d", fired)
	}
}
