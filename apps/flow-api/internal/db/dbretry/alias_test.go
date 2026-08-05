package dbretry

import (
	"context"
	"testing"

	shared "github.com/libraz/nodate-flow/packages/go-shared/dbretry"
)

// TestAliasesShareTheSharedCollector is the point of this package now
// that the implementation lives in packages/go-shared/dbretry: a hook
// registered through flow-api's alias must be visible to the shared
// package and vice versa.
//
// Re-declaring the collector here instead of aliasing would compile and
// pass every local test while silently splitting the fan-out in two —
// flow-api's eventbus deferring to one context key, the cross-service
// eventlog to another — and whichever appender lost the coin toss would
// deliver nothing. The behaviour of the hooks themselves is covered in
// the shared package alongside the code.
func TestAliasesShareTheSharedCollector(t *testing.T) {
	t.Parallel()

	ctx := WithCommitHooks(context.Background())
	if !shared.HasCommitHooks(ctx) {
		t.Fatal("a collector attached through the alias must be visible to the shared package")
	}

	sharedCtx := shared.WithCommitHooks(context.Background())
	if !HasCommitHooks(sharedCtx) {
		t.Fatal("a collector attached by the shared package must be visible through the alias")
	}

	fired := 0
	AddCommitHook(ctx, func() { fired++ })
	if fired != 0 {
		t.Fatalf("a hook on a collector context must be deferred, fired = %d", fired)
	}

	// No collector: the row is already durable, so the hook runs now.
	AddCommitHook(context.Background(), func() { fired++ })
	if fired != 1 {
		t.Fatalf("a hook with no enclosing transaction must fire immediately, fired = %d", fired)
	}
}
