package kindscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// TestUndeclaredRefusesADeclaredKind covers the run-time half of the
// escape's contract, which is the half that has to answer for values the
// scanner cannot: a kind assembled from a variable is invisible to a
// static check, and the escape is worth nothing if it accepts a declared
// kind by that route.
func TestUndeclaredRefusesADeclaredKind(t *testing.T) {
	t.Parallel()

	// Through a variable on purpose. A constant here would be reported by
	// the guard scanning this package — correctly — and would leave the
	// run-time check untested.
	declared := string(eventbus.TaskCreated)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Undeclared must refuse a declared kind, it returned instead")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value must be a string, got %T", r)
		}
		if !strings.Contains(msg, declared) {
			t.Errorf("the panic must name the offending kind, got %q", msg)
		}
	}()
	kindscan.Undeclared(declared)
}

// TestUndeclaredAcceptsAKindNothingDeclares is the other side: the
// values the escape exists for pass through unchanged.
func TestUndeclaredAcceptsAKindNothingDeclares(t *testing.T) {
	t.Parallel()

	const want = "nothing.declares.this"
	got := kindscan.Undeclared(want)
	if string(got) != want {
		t.Fatalf("Undeclared must return the kind as given, got %q", got)
	}
}

// TestScanTellsTheEscapeApartFromTheAbuse is the static half. The three
// forms in the testdata package look almost identical in source and must
// be answered differently: a bare literal and a declared kind smuggled
// through the escape are both reported, and only the kind no constant
// covers is allowed.
func TestScanTellsTheEscapeApartFromTheAbuse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	cache := payloadscan.NewExportCache()
	if err := cache.Warm(moduleRoot(t)); err != nil {
		t.Fatalf("warm export cache: %v", err)
	}
	findings, err := kindscan.Scan(kindscan.Config{
		Dir:   filepath.Join("testdata", "undeclaredabuse"),
		Cache: cache,
	})
	if err != nil {
		t.Fatalf("scan testdata: %v", err)
	}

	byValue := map[string]kindscan.Finding{}
	for _, f := range findings {
		byValue[f.Value] = f
	}
	if len(findings) != 2 {
		t.Fatalf("want the two rejected forms reported, got %d: %v", len(findings), findings)
	}

	laundered, ok := byValue["task.created"]
	if !ok {
		t.Fatal("a declared kind passed to Undeclared must still be reported")
	}
	if !laundered.ViaUndeclared {
		t.Error("the escape abuse must be reported as such, not as a bare literal")
	}
	if !strings.Contains(laundered.String(), "kindscan.Undeclared") {
		t.Errorf("the message must point at the escape, got %q", laundered.String())
	}

	bare, ok := byValue["task.updated"]
	if !ok {
		t.Fatal("a bare literal must still be reported")
	}
	if bare.ViaUndeclared {
		t.Error("a bare literal must not be attributed to the escape")
	}

	if _, reported := byValue["nothing.declares.this"]; reported {
		t.Error("a kind nothing declares is what the escape is for; it must pass")
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
