package kindscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// writeGoFile drops one source file into dir, creating it if needed. The
// file is never compiled — [kindscan.Packages] decides by reading source,
// so a tree on disk is all the walk needs.
func writeGoFile(t *testing.T, dir, name, src string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// fixtureField is the kind-bearing field of the testdata struct. A type
// declared in the package being scanned is spelled by the package's name,
// not by an import path, because that is the path the scan type-checks it
// under.
var fixtureField = kindscan.KindField{Type: "kindfieldabuse.AppendEventParams", Field: "Type"}

// scanFixture runs the origin rule over the testdata package with the
// fixture's field in place of the generated ones.
func scanFixture(t *testing.T) []kindscan.Finding {
	t.Helper()

	cache := payloadscan.NewExportCache()
	if err := cache.Warm(moduleRoot(t)); err != nil {
		t.Fatalf("warm export cache: %v", err)
	}
	findings, err := kindscan.Scan(kindscan.Config{
		Dir:    filepath.Join("testdata", "kindfieldabuse"),
		Cache:  cache,
		Fields: []kindscan.KindField{fixtureField},
	})
	if err != nil {
		t.Fatalf("scan testdata: %v", err)
	}
	return findings
}

// TestScanReportsAKindThatWasNeverOne covers the rule's whole point: the
// column is text, so every one of these forms compiles and inserts, and
// the type system has nothing to say about any of them. Each has to be
// reported by the field it was written to.
func TestScanReportsAKindThatWasNeverOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	byValue := map[string]kindscan.Finding{}
	for _, f := range scanFixture(t) {
		byValue[f.Value] = f
	}

	for _, want := range []string{`"calendar.reminder"`, "localType"} {
		f, ok := byValue[want]
		if !ok {
			t.Fatalf("a write of %s to %s must be reported, got %v", want, fixtureField, byValue)
		}
		if f.Field != fixtureField.String() {
			t.Errorf("the finding must name the field written to, got %q", f.Field)
		}
		if !strings.Contains(f.String(), fixtureField.String()) {
			t.Errorf("the message must name the field, got %q", f.String())
		}
	}
}

// TestScanReportsEveryFormOfTheWrite pins the count, because a rule that
// covers the keyed literal and nothing else reads as coverage while the
// same write one line further down goes through: the fixture holds a
// keyed literal, an unkeyed one, a local constant and a later assignment,
// and exactly those four must be reported.
func TestScanReportsEveryFormOfTheWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	findings := scanFixture(t)
	if len(findings) != 4 {
		t.Fatalf("want the four rejected writes reported, got %d: %v", len(findings), findings)
	}
}

// TestScanAcceptsAValueThatWasAKind is the other side. A conversion from
// Kind is the sanctioned form and must pass, including through the
// escape — which is the only route to a kind no constant covers, and
// which refuses a declared one on its own.
func TestScanAcceptsAValueThatWasAKind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	for _, f := range scanFixture(t) {
		if strings.Contains(f.Value, "eventbus.CalendarReminder") ||
			strings.Contains(f.Value, "string(kind)") ||
			strings.Contains(f.Value, "kindscan.Undeclared") {
			t.Errorf("a value that was a kind must pass, got %q", f.String())
		}
	}
}

// TestPackagesReachAFileThatNeverNamesTheEventbus proves the walk covers
// the file this rule exists for. The scheduler that wrote
// `calendar.reminder` mentioned no eventbus symbol at all, so narrowing
// the walk by that word alone left it unscanned — and a package the scan
// never opens reports the same nothing as one that is clean.
func TestPackagesReachAFileThatNeverNamesTheEventbus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pkg := filepath.Join(dir, "appender")
	writeGoFile(t, pkg, "appender.go", `package appender

type params struct{ Type string }

func Append() params { return params{Type: "calendar.reminder"} }
`)

	dirs, err := kindscan.Packages(dir)
	if err != nil {
		t.Fatalf("packages: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("a package naming neither the eventbus nor a kind-bearing struct is not worth checking, got %v", dirs)
	}

	writeGoFile(t, pkg, "write.go", `package appender

import "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"

func Params() generated.AppendEventParams { return generated.AppendEventParams{} }
`)

	dirs, err = kindscan.Packages(dir)
	if err != nil {
		t.Fatalf("packages: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != pkg {
		t.Fatalf("the walk must reach a package that writes a kind-bearing struct, got %v", dirs)
	}
}
