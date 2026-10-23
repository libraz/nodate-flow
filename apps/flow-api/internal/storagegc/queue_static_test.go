package storagegc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// blobOwners are the directories allowed to call RemoveObject directly:
// the client itself and this package, which removes only queued keys.
var blobOwners = []string{"storage", "storagegc"}

// TestEveryUnreferencedDeleteQueuesItsBlob proves that every flow-api
// function forgetting a storage_objects row also records the blob in
// storage_blob_removals, and that nothing outside the owners removes a
// blob without the record. A direct RemoveObject after commit orphans the
// blob for good when it fails; the queue is what the sweeper retries.
func TestEveryUnreferencedDeleteQueuesItsBlob(t *testing.T) {
	t.Parallel()

	root := ".."
	fset := token.NewFileSet()
	deleters := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == filepath.Join("db", "generated") || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		owner := false
		for _, o := range blobOwners {
			if filepath.Dir(rel) == o {
				owner = true
			}
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var deletes, queues bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "DeleteStorageObjectIfUnreferenced":
					deletes = true
				case "Queue":
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "storagegc" {
						queues = true
					}
				case "RemoveObject":
					if !owner {
						t.Errorf("%s: %s calls RemoveObject directly; queue the key with storagegc.Queue in the deleting transaction and remove it with storagegc.RemoveQueued",
							fset.Position(call.Pos()), fn.Name.Name)
					}
				}
				return true
			})
			if deletes {
				deleters++
				if !queues && !owner {
					t.Errorf("%s: %s deletes an unreferenced storage object without storagegc.Queue; the blob is orphaned if the removal after commit fails",
						fset.Position(fn.Pos()), fn.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if deleters == 0 {
		t.Fatal("no DeleteStorageObjectIfUnreferenced callers found; the guard is looking at nothing")
	}
}
