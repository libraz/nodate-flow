package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestWritesThisCheckCannotReadAreDeclared refuses a write to a table the
// rules govern whose columns the source does not state.
//
// A derivation that skipped one would report full coverage of a set it
// knows it did not read, and the gap would be invisible in exactly the
// place a gap matters: the write is on the rules' own table, so whether a
// rule applies to it is a live question and the answer is "nobody can
// tell from here". Either the statement is spelled out — in sql/queries,
// or as a literal a reader can attribute — or the fact that it is not is
// written down.
func TestWritesThisCheckCannotReadAreDeclared(t *testing.T) {
	t.Parallel()

	src, _ := load(t)
	writes := UnattributableWrites(src, Rules)

	for _, w := range writes {
		if coveredBy(w) != nil {
			continue
		}
		t.Errorf("%s: %s assembles a write to %s whose columns are not in the source, so no "+
			"column-scoped rule can decide whether it is in scope: %q.\n"+
			"  Move the statement to sql/queries, or spell it as one literal per column, or "+
			"declare it in UnattributableExceptions with the reason its columns cannot be read",
			w.Location(), w.Symbol, w.Table, w.Text)
	}
}

// TestUnattributableExceptionsStillCoverSomething refuses an exception
// that names nothing.
//
// The list is the record of what this check does not read, so an entry
// that covers no write is worse than no entry: a reader finds a write
// named and accounted for, when the write may have been rewritten, moved,
// or removed entirely. The file has to exist, the write has to still be
// there, and the reason has to say something.
func TestUnattributableExceptionsStillCoverSomething(t *testing.T) {
	t.Parallel()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	src, _ := load(t)
	writes := UnattributableWrites(src, Rules)

	if len(UnattributableExceptions) == 0 {
		t.Skip("nothing is declared unreadable")
	}

	for _, e := range UnattributableExceptions {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(e.File))); statErr != nil {
			t.Errorf("the exception for %q names %s, which is not a file in this repository; "+
				"an exception that names nothing reads as though a write was accounted for",
				e.Prefix, e.File)
			continue
		}
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("the exception for %s in %s states no reason; the reason is the whole "+
				"content of the entry", e.Prefix, e.File)
		}
		covered := 0
		for _, w := range writes {
			if e.Covers(w) {
				covered++
			}
		}
		if covered == 0 {
			t.Errorf("the exception for %q in %s covers no write this check could not read; "+
				"the write it was written for has been moved, rewritten or removed, so drop it",
				e.Prefix, e.File)
		}
	}
}

// TestUnreadableWriteDetectionSeesEachShape is the positive control.
//
// A detector that stops matching reports nothing, which is
// indistinguishable from a tree with nothing left to report — and the
// whole point of this pair of checks is that "nothing to report" has to
// be earned. So the walk is pointed at a tree holding each shape it must
// name and each near miss it must not: a write assembled by
// concatenation, one assembled through a format verb, a write to a table
// no rule governs, a write spelled out in full, and a sentence describing
// one.
func TestUnreadableWriteDetectionSeesEachShape(t *testing.T) {
	t.Parallel()

	root := writeUnreadableControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	var got []string
	for _, w := range UnattributableWrites(src, Rules) {
		got = append(got, w.Symbol[strings.LastIndex(w.Symbol, ".")+1:])
	}
	sort.Strings(got)

	want := []string{"concatenatesColumn", "substitutesPredicate"}
	if len(got) != len(want) {
		t.Fatalf("the walk named %v, want %v; a spelled-out write, a write to a table no rule "+
			"governs and a sentence about a write are not unreadable", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("named %d is %q, want %q", i, got[i], want[i])
		}
	}

	// The other half of the same fixture: a write assembled from fragments
	// is not silently derived as a sink either, because the fragment's
	// column list is not the statement's.
	sinks := Sinks(src, nil, Rules[2])
	if Rules[2].Name != "date-order" {
		t.Fatalf("the third rule is %q; this control is about the task date rule", Rules[2].Name)
	}
	for key := range sinks {
		if strings.HasSuffix(key, ".concatenatesColumn") {
			t.Error("a write whose column is concatenated in was derived as a sink; " +
				"the fragment in the source names no column, so nothing decided this")
		}
	}
	if _, ok := sinks[modulePath+"/internal/itemkit.writesTheColumnPlainly"]; !ok {
		t.Error("the write spelled out in one literal is not derived as a sink; " +
			"the literal half of the sink set has stopped matching")
	}
}

// coveredBy returns the exception covering a write, or nil.
func coveredBy(w UnattributableWrite) *UnattributableException {
	for i := range UnattributableExceptions {
		if UnattributableExceptions[i].Covers(w) {
			return &UnattributableExceptions[i]
		}
	}
	return nil
}

// writeUnreadableControlTree lays out a minimal apps/flow-api/internal
// tree holding one of each shape, and returns the root [Parse] would be
// given.
func writeUnreadableControlTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	path := filepath.Join(root, "apps", "flow-api", "internal", "itemkit", "writes.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `package itemkit

import "fmt"

// concatenatesColumn builds the column name into the statement, so the
// table reaches the source and the column does not.
func concatenatesColumn(ctx Ctx, tx TX, col string) error {
	q := "UPDATE tasks SET " + col + " = ? WHERE id = ?"
	_, err := tx.ExecContext(ctx, q, nil, 0)
	return err
}

// substitutesPredicate fills the rest of the statement in through a
// format verb.
func substitutesPredicate(ctx Ctx, tx TX, where string) error {
	q := fmt.Sprintf("UPDATE calendar_events SET start_at = ? WHERE %s", where)
	_, err := tx.ExecContext(ctx, q, nil)
	return err
}

// writesTheColumnPlainly spells the whole statement out, so it is read
// rather than reported.
func writesTheColumnPlainly(ctx Ctx, tx TX) error {
	_, err := tx.ExecContext(ctx, "UPDATE tasks SET due_on = ? WHERE id = ?", nil, 0)
	return err
}

// buildsElsewhere assembles a write to a table no rule governs, which is
// not this check's territory.
func buildsElsewhere(ctx Ctx, tx TX, col string) error {
	_, err := tx.ExecContext(ctx, "UPDATE agent_runs SET "+col+" = ? WHERE id = ?", nil, 0)
	return err
}

// describesAWrite says UPDATE tasks SET due_on = ? in prose and issues
// nothing.
func describesAWrite(ctx Ctx) error {
	return nil
}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root
}
