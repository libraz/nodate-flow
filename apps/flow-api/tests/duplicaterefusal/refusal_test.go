package duplicaterefusal

import (
	"sort"
	"strings"
	"testing"
)

// TestNamedDuplicateRefusalsNameAConflictTheSchemaCanRaise holds every
// branch that translates a duplicate-entry error into a named refusal to
// the table it writes.
//
// The refusal is a claim about the database: it says a duplicate key on
// this write means one specific thing, and the API publishes that meaning
// as a code a client is told to act on. The claim is true only if the table
// carries a key the caller's own input can collide. Where it does not, the
// branch is dead — and on the one occasion it could fire, it would report a
// retryable identifier collision as a permanent conflict over the caller's
// input, which is the inversion the branch was written to prevent.
func TestNamedDuplicateRefusalsNameAConflictTheSchemaCanRaise(t *testing.T) {
	t.Parallel()

	_, statements, tables, placed, _ := load(t)

	for _, a := range placed {
		table, known := tables[a.Table]
		if !known {
			t.Errorf("%s guards %s, which writes %q, but no CREATE TABLE under sql/core or sql/flow declares that table.\n"+
				"  Either the statement writes something the schema files do not carry, or the table reader has stopped matching",
				a.Branch.Location(), a.Query.Name, a.Table)
			continue
		}
		if a.Query.SuppressesDuplicate() {
			t.Errorf("%s translates a duplicate key on %s (%s), but that statement asks MySQL not to raise one — "+
				"an INSERT IGNORE or an ON DUPLICATE KEY UPDATE returns no error, so the branch is unreachable.\n"+
				"  Drop the branch, or drop the suppression so the conflict reaches the caller as the refusal this names",
				a.Branch.Location(), a.Query.Name, a.Query.Location())
			continue
		}
		if len(table.Collidable()) > 0 {
			continue
		}
		t.Errorf("%s translates a duplicate key on %s into a named refusal, but %s (%s) carries no key a caller can collide.\n"+
			"  Its only unique keys are %s — every one of them over an identifier the server issues rather than a value the caller sent — "+
			"so the branch is unreachable, and if it ever did fire it would report an identifier collision, which is retryable, "+
			"as a permanent conflict over the caller's input.\n"+
			"  Add the key the refusal names, or drop the branch and let the write report the fault it actually had",
			a.Branch.Location(), a.Query.Name, table.Name, table.Location(), renderKeys(table.Identifying()))
	}

	// A derived check that stops matching reports nothing rather than
	// reporting a problem, so what it covered is asserted before an empty
	// finding list is read as a pass.
	if len(statements) == 0 {
		t.Fatal("no statement was read from sql/queries; every branch would be unattributable for the same wrong reason")
	}
	if len(tables) == 0 {
		t.Fatal("no table was read from sql/core or sql/flow; every branch would look defenceless")
	}
	byApp := map[string]int{}
	for _, a := range placed {
		byApp[a.Branch.App]++
	}
	for _, app := range []string{"flow-api", "auth-api"} {
		if byApp[app] == 0 {
			t.Errorf("no duplicate-entry branch was placed in %s; both services translate this error into their own "+
				"codes, so a walk that reads one of them calls the other clean", app)
		}
	}
}

// TestEveryDuplicateBranchResolvesToTheWriteItGuards refuses a branch whose
// write this check cannot name.
//
// A branch it cannot place is not evidence of anything, and skipping one
// would let the check report full coverage of a set it did not read — which
// is how a dead refusal survives in the first place. Either the write is
// resolvable from the branch, or the fact that it is not is written down
// with the reason.
func TestEveryDuplicateBranchResolvesToTheWriteItGuards(t *testing.T) {
	t.Parallel()

	root, _, _, _, unresolved := load(t)

	for _, u := range unresolved {
		if ExceptionFor(u.Branch) != nil {
			continue
		}
		t.Errorf("%s in %s translates a duplicate key into a named refusal, but this check cannot say which table it writes: %s.\n"+
			"  Bind the statement's error where the branch reads it, or declare the site in AttributionExceptions with the reason its write cannot be resolved",
			u.Branch.Location(), u.Branch.Func, u.Why)
	}

	for _, e := range AttributionExceptions {
		if problem := e.Problem(root, unresolved); problem != "" {
			t.Errorf("the exception for %s in %s %s", e.Func, e.File, problem)
		}
	}
}

// TestDuplicateBranchInventoryIsReported prints what the derivation placed
// and what it did not.
//
// The report is the part of a derived check that is worth reading on a
// passing run: it says which branches were held to the schema and through
// which statement, so a walk that quietly stops matching shows up as a
// shrinking inventory rather than as continued silence.
func TestDuplicateBranchInventoryIsReported(t *testing.T) {
	t.Parallel()

	_, _, tables, placed, unresolved := load(t)

	t.Logf("placed %d duplicate-entry branches, unresolved %d", len(placed), len(unresolved))
	for _, a := range placed {
		table := tables[a.Table]
		via := ""
		if a.Indirect {
			via = " (through a function value)"
		}
		t.Logf("  %s -> %s writes %s, collidable keys: %s%s",
			a.Branch.Location(), a.Query.Name, a.Table, renderKeys(table.Collidable()), via)
	}
	for _, u := range unresolved {
		t.Logf("  %s UNRESOLVED: %s", u.Branch.Location(), u.Why)
	}
}

// renderKeys spells a key list the way a failure quotes it.
func renderKeys(keys []Key) string {
	if len(keys) == 0 {
		return "none"
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Render())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// load reads the SQL and the source once per test, failing loudly on an
// empty read rather than letting the checks range over nothing.
func load(t *testing.T) (root string, statements map[string]Statement, tables map[string]Table, placed []Attribution, unresolved []Unresolved) {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	statements, err = Statements(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	tables, err = ReadTables(root)
	if err != nil {
		t.Fatalf("read sql/core and sql/flow: %v", err)
	}
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse the service trees: %v", err)
	}
	placed, unresolved = Attribute(src, statements)
	if len(placed)+len(unresolved) == 0 {
		t.Fatal("no duplicate-entry branch was found in either service; the walk has stopped matching " +
			"rather than the branches having gone away")
	}
	return root, statements, tables, placed, unresolved
}
