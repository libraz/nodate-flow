package airequest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannedSymbol is the composite literal that lets a call site assemble a
// request field by field — and therefore omit a field. Every one of the
// three settings this package exists to carry was lost that way.
const bannedSymbol = "providers.Request{"

// exemptDirs are the directories allowed to name the literal: this
// package, which owns it. The providers package itself writes the
// literal unqualified (Request{...}) and so never matches. Paths are
// slash-separated and matched as prefixes relative to the flow-api
// module root.
var exemptDirs = []string{
	"internal/ai/airequest",
}

// TestRequestConstructionCentralized proves airequest is the only way a
// providers.Request gets built.
//
// The guard is "no other file may write the literal at all" rather than
// "the known call sites look right" because the defect was never one
// mistake in one place: thirteen call sites each hand-built a request,
// and every one of them omitted the model. A reviewer reading any single
// one sees nothing wrong — the omission is only visible against a
// definition of what a complete request is, which is what this package
// now holds.
//
// Read the guarantee narrowly. It catches the literal, which is how the
// omission actually happened; it cannot catch a caller that declares a
// zero-valued request and assigns fields to it. That would be a
// deliberate detour rather than an oversight, and the behavioural tests
// in agent_executor_settings_test.go cover what the provider is
// ultimately handed.
func TestRequestConstructionCentralized(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var offenders []string
	var sawSentinel bool

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
		if rel == walkSentinel {
			sawSentinel = true
		}
		// The walk root is this repository's own source tree, supplied by
		// the test, not by anything a caller controls.
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), bannedSymbol) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	requireSentinel(t, sawSentinel)

	if len(offenders) > 0 {
		t.Fatalf("LLM requests must be built through airequest.New or airequest.ForAgent, which name the model and carry the agent's output cap and temperature; "+
			"these files build the request themselves:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestAgentSettingsComeFromTheAgentRow proves production code cannot
// invent an AgentSettings: it has to project the row the query returned.
// A hand-built one would let a caller pass along a model while forgetting
// the temperature, which is the same defect one level up.
func TestAgentSettingsComeFromTheAgentRow(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var offenders []string
	var sawSentinel bool

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
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
		if rel == walkSentinel {
			sawSentinel = true
		}
		b, readErr := os.ReadFile(path) //#nosec G122 -- walk root is the repo source tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), "airequest.AgentSettings{") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	requireSentinel(t, sawSentinel)

	if len(offenders) > 0 {
		t.Fatalf("agent settings must come from airequest.FromExecRow so every column the query selects reaches the request; "+
			"these files assemble them by hand:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// walkSentinel is a file the guards above must have inspected. Both are
// "no file does X" assertions, which a walk that silently stopped
// matching anything would satisfy just as well as a clean tree. Checking
// that a known call site was actually read turns that failure mode from
// a quiet pass into a loud one.
const walkSentinel = "internal/ai/tasks.go"

func requireSentinel(t *testing.T, saw bool) {
	t.Helper()
	if !saw {
		t.Fatalf("the walk never reached %s, so it proved nothing; "+
			"the module layout moved and this guard needs its sentinel updated", walkSentinel)
	}
}

// TestFromExecRowForwardsEveryConfiguredKnob pins the projection itself.
// Deleting a line from FromExecRow would leave both guards above green
// while restoring the original defect, so the mapping is asserted
// directly.
func TestFromExecRowForwardsEveryConfiguredKnob(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("airequest.go")
	if err != nil {
		t.Fatalf("read airequest.go: %v", err)
	}
	body, ok := sliceBetween(string(src), "func FromExecRow(", "\n}\n")
	if !ok {
		t.Fatal("could not locate FromExecRow")
	}
	for _, want := range []string{"row.ModelName", "row.Temperature", "row.MaxOutputTokens"} {
		if !strings.Contains(body, want) {
			t.Errorf("FromExecRow must forward %s; without it the column is selected by the query and then discarded", want)
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

// moduleRoot returns the apps/flow-api directory. Tests run in the
// package directory, so the module root is three levels up from
// internal/ai/airequest.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the flow-api module root at %s: %v", root, err)
	}
	return root
}
