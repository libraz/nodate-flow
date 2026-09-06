package eventkinds

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// TestNoUndeclaredEventKindsInSQL proves nothing under sql/ spells an
// event kind the registry does not declare.
//
// A kind written into SQL is invisible to every check the Go side has.
// Nothing in Go refers to the string, so renaming the constant compiles,
// passes vet, passes the literal scan, and leaves the statement naming a
// kind that no longer exists — which inserts rows nobody subscribes to,
// or filters on a name no row carries. Both read as an empty result, not
// as an error.
//
// The whole tree is scanned, not only sql/queries. A view that filters on
// a kind, a trigger that appends one and a conformance fixture that seeds
// one are the same hazard as a query, and none of them is reached by
// anything else. Nothing is excluded, including the generated
// sql/schema.sql: an exclusion list is one more thing that goes stale
// without saying so, and the cost of scanning a generated file is at
// worst one duplicate message on a day something is already wrong.
func TestNoUndeclaredEventKindsInSQL(t *testing.T) {
	msgs, err := kindscan.ScanSQL(filepath.Join(repoRoot(t), "sql"))
	if err != nil {
		t.Fatalf("scan sql: %v", err)
	}
	for _, msg := range msgs {
		t.Error(msg)
	}
}

// repoRoot returns the directory holding go.work, which is where the
// query tree lives — outside any one module, because the schema is shared
// by all of them.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir := moduleRoot(t)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found above the module directory")
		}
		dir = parent
	}
}
