package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunAddFavoriteValidatesTargetBeforeInsert requires runAddFavorite to
// confirm the target row exists before it writes the favorite, so a favorite
// cannot be created pointing at a project, task or page the caller cannot
// reach — or at nothing at all.
//
// The file holding the function is located by searching the package rather
// than named here. A named file couples the check to where the function
// happens to live: moving it to another file in the same package leaves the
// behaviour intact and the check reading a path that no longer exists, which
// is a failure that says nothing about favorites.
func TestRunAddFavoriteValidatesTargetBeforeInsert(t *testing.T) {
	body := functionBodyInPackage(t, "runAddFavorite")

	targetCheck := "ensureMCPFavoriteTargetExists(ctx, deps, s, tt, in.TargetID, targetPub)"
	insert := "deps.Queries.CreateFavorite"

	checkAt := strings.Index(body, targetCheck)
	insertAt := strings.Index(body, insert)
	if checkAt < 0 {
		t.Fatal("runAddFavorite must validate that the target exists")
	}
	if insertAt < 0 {
		t.Fatal("runAddFavorite must insert the favorite; the insert this check orders against is gone")
	}
	if checkAt > insertAt {
		t.Fatal("runAddFavorite must validate the target before inserting the favorite")
	}
}

// functionBodyInPackage returns the source text of the named top-level
// function, found by scanning the package directory.
//
// Exactly one file must declare it: none means the function was renamed or
// removed and the caller is asserting against nothing, and more than one is
// not buildable, so either way the ambiguity is reported rather than guessed
// past.
func functionBodyInPackage(t *testing.T, name string) string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	decl := "\nfunc " + name + "("
	var found []string
	var body string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(filepath.Clean(e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		start := strings.Index(string(src), decl)
		if start < 0 {
			continue
		}
		found = append(found, e.Name())
		rest := string(src)[start+1:]
		// The body runs to the next top-level declaration, so the
		// assertions above are about this function and not about
		// whatever happens to sit below it in the same file.
		if end := strings.Index(rest, "\nfunc "); end >= 0 {
			rest = rest[:end]
		}
		body = rest
	}

	switch len(found) {
	case 1:
		return body
	case 0:
		t.Fatalf("no file in this package declares %s; it was renamed or removed, and what this test guards is no longer being checked anywhere", name)
	default:
		t.Fatalf("%d files declare %s (%s); the package does not build, so the check cannot say which body it read", len(found), name, strings.Join(found, ", "))
	}
	return ""
}
