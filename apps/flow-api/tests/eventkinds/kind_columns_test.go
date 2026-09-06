package eventkinds

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// kindBearingColumns are the columns whose values are event kinds.
//
// The SQL scanner matches a literal by the column name beside it and
// resolves no table, which is only safe while these are the only columns
// named `type` or `event_type` in the schema. That is a fact about the
// schema, and it can stop being true without anyone touching the scanner
// that leans on it.
var kindBearingColumns = map[string]bool{
	"events.type":                   true,
	"notifications.event_type":      true,
	"webhook_deliveries.event_type": true,
}

// schemaDirs are the layers build-schema.sh assembles, which between them
// hold every table the product defines.
func schemaDirs(t *testing.T) []string {
	t.Helper()

	root := repoRoot(t)
	return []string{
		filepath.Join(root, "sql", "core"),
		filepath.Join(root, "sql", "flow"),
	}
}

// TestEveryKindColumnIsClassified holds the scanner's column rule to the
// schema, in both directions.
//
// A column named `type` or `event_type` that is not a kind means the
// scanner will reject correct queries written against it — the failure
// most likely to get a guard switched off rather than fixed. A kind
// column the scanner stops matching means it quietly covers less than it
// says. Neither shows up anywhere else: the scanner keeps compiling, and
// on the day it breaks it breaks in somebody else's change.
func TestEveryKindColumnIsClassified(t *testing.T) {
	for _, msg := range classifyKindColumns(t, schemaDirs(t)) {
		t.Error(msg)
	}
}

// TestTheNeighbouringTypeColumnsAreNotMatched pins the case a loosening
// breaks first.
//
// Eight columns in the schema end in _type and hold ordinary words —
// task, project, page, burndown, MIME types. notifications carries
// event_type and resource_type on adjacent lines, so one statement can
// name both, and a rule widened to a _type suffix would report the second
// while the query is exactly right.
func TestTheNeighbouringTypeColumnsAreNotMatched(t *testing.T) {
	var neighbours []kindscan.Column
	for _, dir := range schemaDirs(t) {
		columns, err := kindscan.SchemaColumns(dir)
		if err != nil {
			t.Fatalf("read schema %s: %v", dir, err)
		}
		for _, c := range columns {
			if strings.HasSuffix(c.Name, "_type") && !kindBearingColumns[c.Qualified()] {
				neighbours = append(neighbours, c)
			}
		}
	}

	// A rule proved against no neighbour is proved against nothing, and a
	// schema reader that quietly returns none reads exactly like a schema
	// that has none.
	if len(neighbours) == 0 {
		t.Fatal("no _type column outside the kind-bearing set was found; the rule this pins would be untested")
	}
	for _, c := range neighbours {
		if kindscan.IsKindColumn(c.Name) {
			t.Errorf("%s: %s holds no event kind, and the scanner matches it; "+
				"a literal written to it would be reported against a query that is correct", c.Pos, c.Qualified())
		}
	}
}

// classifyKindColumns reports every column the schema and the scanner
// disagree about, and every classified column the schema no longer has.
func classifyKindColumns(t *testing.T, dirs []string) []string {
	t.Helper()

	var msgs []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		columns, err := kindscan.SchemaColumns(dir)
		if err != nil {
			t.Fatalf("read schema %s: %v", dir, err)
		}
		for _, c := range columns {
			qualified := c.Qualified()
			classified := kindBearingColumns[qualified]
			if classified {
				seen[qualified] = true
			}
			switch {
			case kindscan.IsKindColumn(c.Name) && !classified:
				msgs = append(msgs, fmt.Sprintf("%s: %s is named for an event kind and is not classified as one; "+
					"either it carries kinds — add it to kindBearingColumns — or it does not, and the SQL scanner in "+
					"packages/go-shared/kindscan has to stop matching a bare column name before this column can be queried",
					c.Pos, qualified))
			case classified && !kindscan.IsKindColumn(c.Name):
				msgs = append(msgs, fmt.Sprintf("%s: %s carries event kinds and the scanner does not match it; "+
					"every literal written to it is unchecked", c.Pos, qualified))
			}
		}
	}
	for qualified := range kindBearingColumns {
		if !seen[qualified] {
			msgs = append(msgs, fmt.Sprintf("%s is classified as carrying event kinds but the schema declares no such column; "+
				"a classification naming nothing classifies nothing", qualified))
		}
	}
	return msgs
}
