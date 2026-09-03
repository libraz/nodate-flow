package eventbus_test

import (
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// TestNoEventKindLiterals proves no package in this module writes an
// event kind as a string literal.
//
// Kind is a defined type, so a string variable cannot be used as an
// event kind. An untyped constant still can — Go converts a literal to
// the defined type implicitly — which is how a kind nobody subscribes to
// gets appended: it compiles, it inserts, and the only symptom is a
// notification that never arrives. The compiler cannot state that half
// of the rule, so this test does, knowing it reports after the fact
// rather than preventing.
//
// This module has no file that legitimately spells a kind out: the
// constants live in packages/go-shared/eventbus and this package
// re-exports them, so the allowlist is empty.
func TestNoEventKindLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}

	msgs, err := kindscan.ScanModule(moduleRoot(t))
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	for _, msg := range msgs {
		t.Error(msg)
	}
}
