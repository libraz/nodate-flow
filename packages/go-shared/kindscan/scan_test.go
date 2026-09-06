package kindscan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// xtestFixture is the import path of the fixture whose external test
// package writes a kind. The scan needs the path spelled out: it is what
// the external package's own import is matched against.
const xtestFixture = "github.com/libraz/nodate-flow/packages/go-shared/kindscan/testdata/xtestabuse"

// TestScanReachesTheExternalTestPackage covers the half of a directory
// the checker cannot be handed in one pass. Files declaring `package
// foo_test` are a package of their own, and skipping them for that reason
// exempts exactly the files whose job is to pin a spelling — a test
// asserting on a kind nothing emits outlives the kind and keeps passing.
func TestScanReachesTheExternalTestPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	cache := payloadscan.NewExportCache()
	if err := cache.Warm(moduleRoot(t)); err != nil {
		t.Fatalf("warm export cache: %v", err)
	}
	findings, err := kindscan.Scan(kindscan.Config{
		Dir:        filepath.Join("testdata", "xtestabuse"),
		ImportPath: xtestFixture,
		Cache:      cache,
	})
	if err != nil {
		t.Fatalf("scan testdata: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("want the one literal in the external test package reported, got %d: %v", len(findings), findings)
	}
	if findings[0].Value != "calendar.subscribed" {
		t.Errorf("the finding must name the literal written, got %q", findings[0].Value)
	}
	if !strings.HasSuffix(filepath.Dir(strings.SplitN(findings[0].Pos, ":", 2)[0]), "xtestabuse") ||
		!strings.Contains(findings[0].Pos, "pin_test.go") {
		t.Errorf("the finding must point at the external test file, got %q", findings[0].Pos)
	}
}

// TestAllowFilesNamesOneFile covers what an exemption is allowed to
// cover. Matching a base name exempts every file that shares it, and the
// name most likely to be reused is kinds.go — the one file whose whole
// point is that literals are legitimate in it. An exemption has to name
// a path.
func TestAllowFilesNamesOneFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping type-checking scan in -short mode")
	}
	t.Parallel()

	cache := payloadscan.NewExportCache()
	if err := cache.Warm(moduleRoot(t)); err != nil {
		t.Fatalf("warm export cache: %v", err)
	}
	scan := func(allow ...string) []kindscan.Finding {
		t.Helper()
		findings, err := kindscan.Scan(kindscan.Config{
			Dir:        filepath.Join("testdata", "undeclaredabuse"),
			Root:       "testdata",
			AllowFiles: allow,
			Cache:      cache,
		})
		if err != nil {
			t.Fatalf("scan testdata: %v", err)
		}
		return findings
	}

	if got := scan(); len(got) != 2 {
		t.Fatalf("the fixture must report its two writes when nothing is exempt, got %v", got)
	}
	if got := scan("undeclaredabuse/abuse.go"); len(got) != 0 {
		t.Errorf("an exemption naming the file's path must cover it, got %v", got)
	}
	if got := scan("abuse.go"); len(got) != 2 {
		t.Errorf("a bare base name must exempt nothing, got %v", got)
	}
	if got := scan("elsewhere/abuse.go"); len(got) != 2 {
		t.Errorf("an exemption naming another directory must not reach this one, got %v", got)
	}
}

// TestScanModuleRefusesAnExemptionThatNamesNothing covers the way an
// allowlist stops working without saying so. A file that moves leaves its
// entry pointing nowhere: the guard keeps passing, and whether it still
// covers what it was meant to is no longer visible from the call.
func TestScanModuleRefusesAnExemptionThatNamesNothing(t *testing.T) {
	t.Parallel()

	if _, err := kindscan.ScanModule(moduleRoot(t), "eventbus/kinds-that-moved.go"); err == nil {
		t.Fatal("an exemption naming no file must be an error, not a silently empty exemption")
	}
}
