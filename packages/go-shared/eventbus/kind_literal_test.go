package eventbus_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// TestNoEventKindLiterals proves no package in this module writes an
// event kind as a string literal.
//
// Kind is a defined type, which stops a string variable from being used
// as an event kind but not an untyped constant: Go converts a literal
// implicitly, so an invented kind compiles, inserts, and is consumed by
// nobody. The compiler cannot state this half of the rule, so it is
// stated here — as a test, which reports after the fact rather than
// preventing, and which covers a package only because the walk reaches
// it. Weaker than a type error on both counts, and still the difference
// between finding these and not.
//
// kinds.go is exempt: it declares the constants, and the declaration is
// the one place the string has to be written out.
func TestNoEventKindLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}

	msgs, err := kindscan.ScanModule(moduleRoot(t), "kinds.go")
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	for _, msg := range msgs {
		t.Error(msg)
	}
}

// moduleRoot returns the directory holding the module's go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
