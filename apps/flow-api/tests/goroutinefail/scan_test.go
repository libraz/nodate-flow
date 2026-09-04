package goroutinefail

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNoGoroutineFailsFromTheWrongGoroutine refuses a `go` statement
// whose body can reach FailNow.
//
// A require in a goroutine has two failure modes and neither reports the
// thing the test was about: while the test is still running the failure
// unwinds a goroutine nobody is waiting on, and once it has returned the
// same call panics and ends the whole binary. The goroutine has to carry
// its outcome back — a channel, a results slice, a struct field — and the
// test goroutine asserts on it.
//
// `go test` throws away a passing package's output, so the target that
// runs this passes -v: the counts below are the evidence that the scan
// read anything, and they are worth nothing unseen.
func TestNoGoroutineFailsFromTheWrongGoroutine(t *testing.T) {
	t.Parallel()

	result := scanRepository(t)
	for _, c := range result.Counts {
		t.Log(c.String())
	}
	for _, f := range result.Findings {
		t.Errorf("%s: this goroutine can end the test from a goroutine the test framework is not "+
			"waiting on (%s). FailNow and SkipNow — and so every require.*, t.Fatal and t.Skip "+
			"— are only legal on the goroutine running the test; elsewhere the outcome is lost, "+
			"and once the test has returned it panics and takes the test binary with it. Return "+
			"the error from the goroutine and assert on it after the wait, or report it with "+
			"t.Errorf, which is legal here",
			f.Location(), f.ChainString())
	}
}

// TestScanSeesAGoroutineReachingFailNow is the positive control, and it
// runs on every invocation: without it a checker that has stopped
// matching reports a clean tree, which is the same output as a clean
// tree.
//
// The sample pins the fixpoint rather than a one-level walk — the flagged
// goroutine reaches require through two helpers, neither of which
// mentions a goroutine — and pins what is not a finding: t.Errorf and
// assert.* record a failure without unwinding and are legal from any
// goroutine.
func TestScanSeesAGoroutineReachingFailNow(t *testing.T) {
	t.Parallel()

	const src = `package p

func sendIt(url string) (int, error) {
	return doRequest(url)
}

func postOrFail(t *testing.T, url string) int {
	status, err := sendIt(url)
	requireOK(t, err)
	return status
}

func requireOK(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}

// TestTwoHops reaches require through two helpers.
func TestTwoHops(t *testing.T) {
	go func() {
		postOrFail(t, "/tasks")
	}()
}

// TestDirectFatal calls the testing handle itself.
func TestDirectFatal(t *testing.T) {
	go func() {
		if err := doIt(); err != nil {
			t.Fatalf("boom: %v", err)
		}
	}()
}

// TestBareCall hands the goroutine a reaching helper directly.
func TestBareCall(t *testing.T) {
	go postOrFail(t, "/tasks")
}

// TestReportsWithErrorf is legal: Errorf does not unwind.
func TestReportsWithErrorf(t *testing.T) {
	go func() {
		if err := doIt(); err != nil {
			t.Errorf("boom: %v", err)
		}
		assert.Equal(t, 1, 1)
	}()
}

// TestCarriesTheOutcomeBack is the shape this check asks for.
func TestCarriesTheOutcomeBack(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := sendIt("/tasks")
		done <- err
	}()
	require.NoError(t, <-done)
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	findings, reach := analyze(fset, []Package{{
		Dir:   "sample",
		Files: []File{{Rel: "sample_test.go", Test: true, AST: file}},
	}})

	if _, ok := reach[funcKey{dir: "sample", name: "postOrFail"}]; !ok {
		t.Error("postOrFail reaches require through requireOK, so the fixpoint must hold it")
	}
	if _, ok := reach[funcKey{dir: "sample", name: "sendIt"}]; ok {
		t.Error("sendIt returns its error and reaches no FailNow, so it must stay out of the set")
	}

	var flagged []string
	for _, f := range findings {
		flagged = append(flagged, f.ChainString())
	}
	want := []string{
		"go func -> postOrFail -> requireOK -> require.NoError",
		"go func -> t.Fatalf",
		"go postOrFail -> requireOK -> require.NoError",
	}
	if !slices.Equal(flagged, want) {
		t.Errorf("flagged %v, want %v", flagged, want)
	}
}

// TestScanRefusesARootThatYieldedNothing pins the non-emptiness proof.
// A checker pointed somewhere with no tests in it reports no findings,
// which is indistinguishable from a clean tree unless the scan says so
// itself.
func TestScanRefusesARootThatYieldedNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pkgDir := filepath.Join(root, "empty", "pkg")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("create sample tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "x.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	result, err := Scan(root, []string{"empty"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("a tree with no tests cannot yield findings, got %d", len(result.Findings))
	}
	err = result.CheckNonEmpty()
	if err == nil {
		t.Fatal("a root with no test file must fail the scan rather than pass it")
	}
	if !strings.Contains(err.Error(), "no *_test.go file") {
		t.Errorf("the failure must name what was missing, got: %v", err)
	}
}

// scanRepository runs the scan against the real tree and refuses a
// verdict from a scan that read nothing.
func scanRepository(t *testing.T) Result {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	result, err := Scan(root, Roots)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := result.CheckNonEmpty(); err != nil {
		t.Fatalf("%v", err)
	}
	return result
}
