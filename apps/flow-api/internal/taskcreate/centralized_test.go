package taskcreate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannedSymbols are the generated identifiers that let a caller hand-build
// a tasks row. Every one of them is reachable only through this package.
//
// The method pattern keeps its leading dot on purpose: without it the check
// would also fire on unrelated declarations whose names merely end in
// CreateTask, such as the MCP tool entry point runCreateTask. The trailing
// paren likewise keeps it off sibling queries like CreateTaskLabel.
var bannedSymbols = []string{
	"CreateTaskParams",
	".CreateTask(",
}

// exemptDirs are the directories allowed to name the generated task-insert
// API: this package, which owns it, and the generated code that defines it.
// Paths are slash-separated and matched as prefixes relative to the
// apps/flow-api module root.
var exemptDirs = []string{
	"internal/taskcreate",
	"internal/db/generated",
}

// TestCreateTaskCentralized proves that taskcreate.New is the only way a
// task row gets written.
//
// The guard exists because the two defects that motivated this package —
// an unallocated task_number and an unset visibility — were not one bug in
// one place. They were the same bug appearing independently at different
// call sites, each of which had hand-built generated.CreateTaskParams and
// each of which forgot a different required column. Centralizing the
// constructor only helps for as long as nothing goes around it, so the
// check is "no other file may name the generated API at all" rather than
// "the known call sites look right".
//
// The walk covers the whole apps/flow-api module rather than one package
// because the call sites live in internal/http/handlers, internal/mcp,
// internal/ai/signaljudge, and cmd/seed-dev. A package-local guard could
// not see across them.
func TestCreateTaskCentralized(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		for _, dir := range exemptDirs {
			if strings.HasPrefix(rel, dir+"/") {
				return nil
			}
		}
		// The walk root is this repository's own source tree, supplied by
		// the test, not by anything a caller controls.
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		src := string(b)
		for _, sym := range bannedSymbols {
			if strings.Contains(src, sym) {
				offenders = append(offenders, rel+" references "+sym)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("task rows must be created through taskcreate.New, which allocates task_number and applies the visibility default; "+
			"these files build the insert themselves:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestNewOwnsTaskNumberAndVisibility pins the two responsibilities the
// constructor exists for. Deleting either one would leave the
// centralization guard above green while reintroducing the original
// defect, so they are asserted directly.
//
// Read the guarantee narrowly: this fixes only that the calls are still
// *present* in the source. It cannot see whether their results are used, so
// a rewrite that allocates a task number and then discards it — or that
// resolves a visibility and then writes something else — passes here. What
// the value is actually used for is covered by the end-to-end tests, which
// read the committed row (see tests/e2e/task_create_persistence_test.go).
func TestNewOwnsTaskNumberAndVisibility(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("taskcreate.go")
	if err != nil {
		t.Fatalf("read taskcreate.go: %v", err)
	}
	for _, want := range []string{
		"tasknumber.Allocate",
		"resolveVisibility",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("taskcreate.New must keep calling %s; without it every caller silently regains the defect it was written to prevent", want)
		}
	}
}

// TestArgsCannotCarryOwnedColumns proves the columns this package owns are
// absent from the caller-facing struct. Re-adding TaskNumber or PublicID to
// Args would make forgetting them possible again.
func TestArgsCannotCarryOwnedColumns(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("taskcreate.go")
	if err != nil {
		t.Fatalf("read taskcreate.go: %v", err)
	}
	argsBody, ok := sliceBetween(string(src), "type Args struct {", "\n}\n")
	if !ok {
		t.Fatal("could not locate the Args struct declaration")
	}
	for _, banned := range []string{"TaskNumber", "PublicID", "DerivedState"} {
		if strings.Contains(argsBody, banned) {
			t.Errorf("Args must not expose %s: it is owned by taskcreate.New so no call site can omit or fabricate it", banned)
		}
	}
}

func sliceBetween(src, openTok, closeTok string) (string, bool) {
	start := strings.Index(src, openTok)
	if start < 0 {
		return "", false
	}
	rest := src[start+len(openTok):]
	end := strings.Index(rest, closeTok)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// moduleRoot returns the apps/flow-api directory. Tests run in the package
// directory, so the module root is two levels up from internal/taskcreate.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the flow-api module root at %s: %v", root, err)
	}
	return root
}
