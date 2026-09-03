// Package commitboundary compiles the programs under testdata/ and
// asserts which of them the type checker refuses.
//
// The rule it guards has no runtime form to test. An event append must
// observe a commit boundary — a transaction that reports its own commit,
// or an explicitly named auto-commit pool — because the fan-out it
// triggers (realtime, in-app notifications, webhook delivery) reads the
// row on another connection. A transaction opened by hand satisfies
// neither: the delivery looks sent and arrives nowhere. A runtime
// backstop or an AST guard is blind through one level of function-call
// facade, so the rule is carried by the type instead —
// dbretry.CommitBoundary, a sealed interface neither *sql.Tx nor *sql.DB
// satisfies.
//
// A property enforced by the compiler is only as durable as the shape of
// the type. Widening dbretry.AutoCommit to accept an interface, or
// adding an implementation inside dbretry that wraps an arbitrary
// handle, takes the wall down with every other test in the tree still
// green. These programs are what notices.
package commitboundary

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// wantMissingMethod names, for each program that must be refused, the
// method whose absence the diagnostic has to blame.
//
// Matching the method rather than the exit status is the point. A
// refused program proves nothing unless it was refused for the reason
// claimed: a typo, a renamed symbol or a stale import fails to compile
// exactly as loudly, and a gate that accepts any failure stays green on
// a tree where the boundary has been widened and something unrelated
// happens to be broken.
var wantMissingMethod = map[string]string{
	// A bare transaction and a bare pool are missing the deferral
	// method, which is the whole contract.
	"reject_hand_rolled_tx": "AfterCommit",
	"reject_bare_db":        "AfterCommit",
	// The look-alike has every exported method, so only the seal is
	// left to refuse it.
	"reject_look_alike": "isCommitBoundary",
}

// mustCompile names the programs that have to build. It is the control
// for everything else here: if the legitimate shapes stop compiling, the
// refusals next door are being produced by a broken module rather than
// by the type.
var mustCompile = []string{"accept_commit_boundary"}

// controlUnrelatedError is a program that fails to compile for a reason
// that is not the boundary. It exercises the gate's own matching.
const controlUnrelatedError = "control_unrelated_error"

// TestBoundaryRefusesUnboundHandles is the gate.
func TestBoundaryRefusesUnboundHandles(t *testing.T) {
	t.Parallel()

	dir := testdataDir(t)
	found := programsUnder(t, dir)

	// The set is checked before anything is compiled. A gate that has
	// quietly stopped finding its programs reports every assertion as
	// passing, which is the failure mode these programs exist to rule
	// out for the code they cover.
	want := make([]string, 0, len(wantMissingMethod)+len(mustCompile)+1)
	for name := range wantMissingMethod {
		want = append(want, name)
	}
	want = append(want, mustCompile...)
	want = append(want, controlUnrelatedError)
	slices.Sort(want)
	if !slices.Equal(found, want) {
		t.Fatalf("testdata no longer holds the programs this gate compiles.\n  found: %s\n  want:  %s\n"+
			"A missing program is not a passing assertion: nothing else in the tree checks that an "+
			"append against an unbound handle fails to build.", strings.Join(found, ", "), strings.Join(want, ", "))
	}

	// Control first. The refusals below mean nothing if the module,
	// the import paths or the toolchain are what is broken.
	for _, name := range mustCompile {
		if err := compile(t, dir, name); err != nil {
			t.Fatalf("%s must compile, and it did not:\n%v\n"+
				"Until it does, the refused programs are not evidence of anything — they would fail "+
				"this way for a broken build too.", name, err)
		}
	}

	for name, method := range wantMissingMethod {
		if err := refused(t, dir, name, method); err != nil {
			t.Errorf("%v", err)
		}
	}
}

// TestGateFailsOnAProgramThatCompiles is the positive control for the
// refusal check: run it against the program that is supposed to build
// and it has to complain.
//
// Without this, a [refused] that had stopped compiling anything — a bad
// path, a swallowed exit code, a matcher that never runs — would report
// every program as correctly refused, and the gate would pass against a
// tree where the boundary no longer exists.
func TestGateFailsOnAProgramThatCompiles(t *testing.T) {
	t.Parallel()

	dir := testdataDir(t)
	if err := refused(t, dir, mustCompile[0], "AfterCommit"); err == nil {
		t.Fatalf("the refusal check reported %s as refused, but that program compiles. "+
			"The check is not compiling anything, so every assertion it makes is vacuous.", mustCompile[0])
	}
}

// TestGateRejectsAnUnrelatedCompileError is the positive control for the
// matching: a program that fails for its own reasons must not be
// accepted as proof about the boundary.
func TestGateRejectsAnUnrelatedCompileError(t *testing.T) {
	t.Parallel()

	dir := testdataDir(t)
	err := refused(t, dir, controlUnrelatedError, "AfterCommit")
	if err == nil {
		t.Fatalf("%s fails to compile for an unrelated reason and the gate accepted it as a "+
			"boundary refusal. The gate is matching the exit status rather than the diagnostic, "+
			"so any breakage in the tree would read as the property holding.", controlUnrelatedError)
	}
	if !strings.Contains(err.Error(), "for the wrong reason") {
		t.Fatalf("the gate refused %s, but not as a wrong-reason failure: %v", controlUnrelatedError, err)
	}
}

// sealMethod is the unexported method that keeps dbretry.CommitBoundary
// unsatisfiable outside its own package.
const sealMethod = "isCommitBoundary"

// refused compiles the named program and returns nil only when the
// compiler rejected it and blamed the given missing method.
func refused(t *testing.T, dir, name, method string) error {
	t.Helper()

	err := compile(t, dir, name)
	if err == nil {
		return &gateError{msg: name + " compiled. An event append against a handle with no observable " +
			"commit is exactly the pairing dbretry.CommitBoundary exists to refuse, and it is now legal " +
			"again: check whether the interface lost its seal, or gained an implementation that wraps an " +
			"arbitrary handle."}
	}
	out := err.Error()
	missing := missingMethods(out)
	if !strings.Contains(out, "does not implement") || len(missing) == 0 {
		return &gateError{msg: name + " was rejected for the wrong reason. The gate needs a " +
			"\"does not implement ... (missing method " + method + ")\" diagnostic; anything else means " +
			"the program is broken rather than the boundary holding:\n" + out}
	}
	if slices.Contains(missing, method) {
		return nil
	}
	// The look-alike carries the interface's exported method set on
	// purpose: only with a complete one is the seal the thing doing the
	// refusing. When a method is added to the boundary this program has
	// to grow it too, or it starts passing for the ordinary reason any
	// incomplete type fails and stops testing the seal at all.
	if method == sealMethod {
		return &gateError{msg: name + " is refused for an incomplete method set (missing " +
			strings.Join(missing, ", ") + ") rather than by the seal, so it no longer tests anything the " +
			"compiler would not have caught anyway. dbretry.CommitBoundary has gained an exported method " +
			"since this program was written: add it to lookAlike, whose whole purpose is to satisfy every " +
			"exported method and still be refused.\n" + out}
	}
	return &gateError{msg: name + " was rejected for the wrong reason. The gate needs a missing " +
		method + "; the compiler blamed " + strings.Join(missing, ", ") + " instead:\n" + out}
}

// missingMethods returns the method names a "missing method X"
// diagnostic blamed, in the order they appear.
func missingMethods(out string) []string {
	const marker = "missing method "
	var names []string
	for rest := out; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return names
		}
		rest = rest[i+len(marker):]
		end := strings.IndexAny(rest, ")\n \t")
		if end < 0 {
			end = len(rest)
		}
		if name := rest[:end]; name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
}

// compile builds one testdata program. The programs are ordinary
// packages rather than commands, so the compiler type-checks them and
// discards the result; nothing is linked and nothing is run.
func compile(t *testing.T, dir, name string) error {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, name, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files under testdata/%s: %v", name, err)
	}
	args := append([]string{"build", "-gcflags=-e"}, files...)
	cmd := exec.Command("go", args...)
	// Run from the module so the import paths and the go.work context
	// resolve the same way they do for the rest of the build.
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return &gateError{msg: strings.TrimSpace(string(out))}
}

// gateError carries compiler output as an error without pulling the
// formatting verbs of fmt.Errorf over multi-line diagnostics.
type gateError struct{ msg string }

func (e *gateError) Error() string { return e.msg }

// programsUnder returns the sorted names of the directories holding the
// gate's programs.
func programsUnder(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

// testdataDir resolves the programs directory from this file's own
// location, so the gate does not depend on the working directory the
// test binary was started in.
func testdataDir(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata")
}

// moduleRoot walks up from this file to the flow-api module directory.
func moduleRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate the flow-api module from %s", filepath.Dir(thisFile))
	return ""
}
