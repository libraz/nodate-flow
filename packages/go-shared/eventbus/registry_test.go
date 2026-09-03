package eventbus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// declaredKindConstants reads kinds.go and returns the identifier and
// string value of every constant declared with the Kind type.
//
// The source is read rather than the package's own values because the
// question is "what constants exist", and a Go program cannot ask that
// of itself. Every guard below rests on it: without an independent
// answer, a registry missing an entry and a registry that is complete
// look identical.
func declaredKindConstants(t *testing.T) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "kinds.go", nil, 0)
	if err != nil {
		t.Fatalf("parse kinds.go: %v", err)
	}

	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Kind" {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s is declared as a Kind but its value is not a string literal", name.Name)
				continue
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				t.Errorf("%s has an unparseable value %s: %v", name.Name, lit.Value, uerr)
				continue
			}
			out[name.Name] = value
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("no Kind constants found in kinds.go; every guard in this file would pass on an empty set")
	}
	return out
}

// TestKindsCoversEveryConstant proves [Kinds] enumerates exactly the
// constants kinds.go declares.
//
// Kinds is what the consumers that must handle all of them iterate. A
// constant left out of it is emitted by handlers, stored in the log, and
// invisible to every table built from the enumeration — which is the
// shape of failure the enumeration exists to end.
func TestKindsCoversEveryConstant(t *testing.T) {
	t.Parallel()

	declared := declaredKindConstants(t)
	enumerated := map[Kind]bool{}
	for _, k := range Kinds() {
		enumerated[k] = true
	}

	for name, value := range declared {
		if !enumerated[Kind(value)] {
			t.Errorf("kinds.go declares %s = %q, which Kinds() does not return; add it to declaredKinds in registry.go", name, value)
		}
	}

	byValue := map[string]string{}
	for name, value := range declared {
		byValue[value] = name
	}
	for k := range enumerated {
		if _, ok := byValue[string(k)]; !ok {
			t.Errorf("Kinds() returns %q, which no constant in kinds.go declares; drop the stale entry from declaredKinds", k)
		}
	}
}

// TestEveryKindResolvesToAFamily proves the family table is total over
// the declared kinds.
//
// The consumers route on the family. One kind that resolves to nothing
// is one kind that reaches the SSE tap and the notification fan-out and
// is silently dropped by both, so the miss has to be a failure here
// rather than an absent notification a month later.
func TestEveryKindResolvesToAFamily(t *testing.T) {
	t.Parallel()

	for name, value := range declaredKindConstants(t) {
		if _, ok := FamilyOf(Kind(value)); !ok {
			t.Errorf("%s = %q belongs to no family; add its namespace to familyPrefix in registry.go", name, value)
		}
	}
}

// TestEveryFamilyHasKinds proves the family table carries no entry that
// nothing resolves to.
//
// A family with no kinds is a prefix that was renamed on one side only.
// It costs nothing at runtime and hides the fact that the kinds it was
// written for now fall somewhere else.
func TestEveryFamilyHasKinds(t *testing.T) {
	t.Parallel()

	used := map[Family]bool{}
	for _, k := range Kinds() {
		f, ok := FamilyOf(k)
		if !ok {
			continue
		}
		used[f] = true
	}
	for _, f := range Families() {
		if !used[f] {
			t.Errorf("family %q matches no declared kind; remove it or fix the prefix", f)
		}
	}
}

// TestFamilyPrefixIsDeclaredForEveryFamily proves the two halves of the
// family declaration — the constant and its prefix — stay together. A
// family constant with no prefix matches nothing, so every kind it was
// meant to cover falls through unclassified.
func TestFamilyPrefixIsDeclaredForEveryFamily(t *testing.T) {
	t.Parallel()

	for _, f := range Families() {
		if f.Prefix() == "" {
			t.Errorf("family %q has no prefix", f)
		}
	}
}

// TestFamilyMatchesLongestPrefix pins the nested-namespace rule.
// ai.suggestion.* and ai.agent.* are separate families with no shared
// parent; if resolution stopped at the first match they would collapse
// into whichever one the map iteration happened to reach first.
func TestFamilyMatchesLongestPrefix(t *testing.T) {
	t.Parallel()

	cases := map[Kind]Family{
		AiSuggestionApplied:     FamilyAiSuggestion,
		AiAgentRunStarted:       FamilyAiAgent,
		AgentTaskThought:        FamilyAgentTask,
		WorkspaceMemberAdded:    FamilyWorkspaceMember,
		CalEventAttendeeAdded:   FamilyCalendar,
		PublicShareCreated:      FamilyPublicShare,
		TaskTransition("start"): FamilyTask,
	}
	for k, want := range cases {
		got, ok := FamilyOf(k)
		if !ok {
			t.Errorf("FamilyOf(%q): no family", k)
			continue
		}
		if got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", k, got, want)
		}
	}
}

// TestJudgeOnlyKindsAreDeclared proves the Applier-reserved set names
// real constants. A typo there disarms the guard for the kind it was
// meant to reserve while still reading as if it were covered.
func TestJudgeOnlyKindsAreDeclared(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, value := range declaredKindConstants(t) {
		declared[value] = true
	}
	reserved := JudgeOnlyKinds()
	if len(reserved) == 0 {
		t.Fatal("no judge-only kinds; the guard that reads this set would let everything through")
	}
	for _, k := range reserved {
		if !declared[string(k)] {
			t.Errorf("judge-only set names %q, which no constant declares", k)
		}
		if !IsJudgeOnly(k) {
			t.Errorf("IsJudgeOnly(%q) = false for a kind the set contains", k)
		}
	}
	if IsJudgeOnly(SignalAttached) {
		t.Error("SignalAttached is reserved for the Applier, but the public ingestion endpoint appends it")
	}
}
