package softdelete

import (
	"strings"
	"testing"
)

// TestEveryRevocableGrantHasARevivalWriter reads the schema and the query
// tree and refuses the combination that makes a removal permanent: a
// tuple keyed without a liveness marker, a statement that revokes it, and
// an insert that neither re-enables on conflict nor is preceded by a
// lookup able to see the revoked row.
//
// The schema conformance suite already refuses `enabled` inside a unique
// index, which is the half a table definition can express. The other half
// lives in the writers and had nothing reading it, so tables kept the
// keys the contract asks for while their inserts collided with their own
// tombstones — add, remove, add returning 500 for good.
//
// Nothing here is listed. The tables come out of sql/schema.sql and the
// evidence out of sql/queries, so a table added tomorrow with the same
// shape is checked without anyone remembering it exists.
func TestEveryRevocableGrantHasARevivalWriter(t *testing.T) {
	t.Parallel()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	tables, err := RevivalTables(root)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	queries, err := Queries(root)
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}

	// A derivation that quietly stops matching passes for the wrong
	// reason, and this one reads files by path. Both sides have to have
	// found something for the result to mean anything.
	if len(tables) == 0 {
		t.Fatal("no table in sql/schema.sql carries a soft-delete flag with a key over a recurring tuple; " +
			"the schema parser has stopped matching rather than the schema having changed")
	}
	if len(queries) == 0 {
		t.Fatal("no statements found under sql/queries")
	}

	scoped := InScope(tables, queries)
	if len(scoped) == 0 {
		t.Fatal("no table both inserts and revokes; the query parser has stopped matching")
	}

	for _, table := range scoped {
		writer := WriterFor(queries, table)
		if writer.Satisfied() {
			continue
		}
		t.Errorf("%s\n"+
			"  keyed on the tuple alone, and %s revokes rows of it, so a revoked row keeps\n"+
			"  holding the tuple. %s inserts beside it and fails on the duplicate key,\n"+
			"  which makes remove-then-add permanently impossible for that pair.\n"+
			"  Either add ON DUPLICATE KEY UPDATE ... enabled = TRUE to the insert, or pair a\n"+
			"  lookup that does not filter on enabled with an UPDATE that re-enables the row\n"+
			"  it finds.",
			Describe(table),
			strings.Join(queryNames(RevokingStatements(queries, table.Name)), ", "),
			strings.Join(queryNames(writer.PlainInserts), ", "))
	}
}

func queryNames(queries []Query) []string {
	out := make([]string, 0, len(queries))
	for _, q := range queries {
		out = append(out, q.Name)
	}
	return out
}
