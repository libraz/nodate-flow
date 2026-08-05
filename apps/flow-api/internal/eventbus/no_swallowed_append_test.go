package eventbus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// swallowedForms are the ways a caller can throw away an append error.
// The blank identifier is the whole problem: it compiles, it reads like
// a decision, and it leaves no reason, no call site and no payload, so
// a dropped row cannot even be reconstructed from the logs afterwards.
//
// Both the qualified spelling (callers outside this package) and the
// bare one (callers inside it) are listed.
var swallowedForms = []string{
	"_ = eventbus.Append(",
	"_ = eventbus.AppendJudgeEvent(",
	"_, _ = eventbus.AppendReverseEvent(",
	"_ = Append(",
	"_ = AppendJudgeEvent(",
	"_, _ = AppendReverseEvent(",
}

// TestNoSwallowedAppends proves every event append in the module either
// propagates its failure or goes through [AppendBestEffort].
//
// The guard is a whole-module walk rather than a package-local check
// because the writers are spread across internal/mcp, internal/ai,
// internal/http/handlers and the workers, and the defect was never one
// call site: the same `_ =` appeared independently in two dozen places
// while fifty-odd neighbours checked the error, so a review-time rule
// had already failed to hold. Dropping a row is not cosmetic — task
// state is derived from the event log (CLAUDE.md rule 8), so a missing
// row is a wrong state that nothing later corrects.
//
// Choosing between the two forms is a real decision; see
// [AppendBestEffort] for the criterion.
func TestNoSwallowedAppends(t *testing.T) {
	t.Parallel()

	root := flowAPIModuleRoot(t)
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
		// The walk root is this repository's own source tree, supplied by
		// the test, not by anything a caller controls.
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		src := string(b)
		for _, form := range swallowedForms {
			if strings.Contains(src, form) {
				offenders = append(offenders, filepath.ToSlash(rel)+" contains "+form)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("an append error may not be discarded: propagate it, or call "+
			"eventbus.AppendBestEffort with a call site so the dropped row is recorded:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestAppendBestEffortStaysAccountable pins the two things that make
// [AppendBestEffort] an acceptable alternative to propagation. Reducing
// it to a silent swallow would leave the guard above green while
// restoring exactly the behaviour it exists to prevent.
func TestAppendBestEffortStaysAccountable(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("append.go")
	if err != nil {
		t.Fatalf("read append.go: %v", err)
	}
	body, ok := sliceBetween(string(src), "func AppendBestEffort(", "\n}\n")
	if !ok {
		t.Fatal("could not locate AppendBestEffort")
	}
	for _, want := range []string{
		"call_site", // who dropped it
		"payload",   // and what the row would have said
	} {
		if !strings.Contains(body, want) {
			t.Errorf("AppendBestEffort must log %s: without it a dropped event cannot be replayed", want)
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

// flowAPIModuleRoot returns the apps/flow-api directory. Tests run in
// the package directory, so the module root is two levels up from
// internal/eventbus.
func flowAPIModuleRoot(t *testing.T) string {
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
