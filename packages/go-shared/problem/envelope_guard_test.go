package problem_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// exemptDirs are the packages allowed to build an HTTP error body
// themselves. Paths are slash-separated, relative to the repository
// root, and matched as prefixes.
var exemptDirs = []string{
	// The writer itself: this is where the envelope is defined.
	"packages/go-shared/problem",
	// MCP speaks JSON-RPC, not problem+json. Its errors are
	// {jsonrpc, id, error:{code, message, data}} frames because that is
	// what an MCP client parses; a problem+json body would be
	// unreadable to it. The nodate-flow error code still travels, in
	// error.data.code.
	"apps/flow-api/internal/mcp",
}

// nonErrorStatuses are the http.Status* constants that do not denote an
// error. Any other Status constant handed to WriteHeader is treated as
// an error status by the guard below.
//
// The list is of the harmless names rather than the harmful ones so an
// unrecognised constant fails the guard instead of slipping past it:
// getting this wrong in the safe direction costs one line here, and in
// the unsafe direction costs a silent hole.
var nonErrorStatuses = map[string]bool{
	"StatusContinue":           true,
	"StatusSwitchingProtocols": true,
	"StatusProcessing":         true,
	"StatusEarlyHints":         true,
	"StatusOK":                 true,
	"StatusCreated":            true,
	"StatusAccepted":           true,
	"StatusNoContent":          true,
	"StatusResetContent":       true,
	"StatusPartialContent":     true,
	"StatusMultiStatus":        true,
	"StatusAlreadyReported":    true,
	"StatusIMUsed":             true,
	"StatusMultipleChoices":    true,
	"StatusMovedPermanently":   true,
	"StatusFound":              true,
	"StatusSeeOther":           true,
	"StatusNotModified":        true,
	"StatusTemporaryRedirect":  true,
	"StatusPermanentRedirect":  true,
}

// bannedMembers are the JSON members of the envelopes this package
// replaced. The SDK reads type / title / detail / status and nothing
// else, so a body offering `code` and `message` reaches it as an error
// with neither a code nor a status.
var bannedMembers = map[string]bool{
	"code":    true,
	"message": true,
}

// TestErrorEnvelopeHasOneWriter proves no code outside [problem] builds
// an HTTP error response body of its own.
//
// Three shapes were in flight at once — problem+json from the handlers,
// {code, message} from the authentication middleware, {status, code,
// message} from the rate limiter — and the SDK understood one of them.
// The authentication middleware is the one that mattered: it guards
// nearly every route, so an expired token arrived at the browser as an
// error carrying no status, which meant neither the dead-session
// handler nor the "do not retry a 4xx" rule fired. The session looked
// alive and the failure was reported as a connection problem.
//
// A guard rather than a fixed set of assertions because three emitters
// drifted apart independently, each one locally reasonable: the ACL
// middleware even copied the struct deliberately, to dodge an import
// cycle, with a comment promising the fields would stay identical. What
// recurs is not a mistake in a known place, it is a new place.
//
// The check is over the parsed syntax, not the source text. A guard
// that grepped would be fooled by a body split across lines and would
// fire on a doc comment quoting the old shape — this repository has
// found seven such holes in text-matching guards.
func TestErrorEnvelopeHasOneWriter(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	var offenders []string
	seen := map[string]bool{}

	forEachPackage(t, root, func(rel string, fset *token.FileSet, files map[string]*ast.File) {
		seen[rel] = true
		if exempt(rel) {
			return
		}
		envelopeTypes := structsWithBannedMembers(files)
		for path, file := range files {
			fileRel := filepath.ToSlash(path)
			for _, fn := range responderBodies(file) {
				offenders = append(offenders,
					handBuiltEnvelopes(fset, fileRel, fn, envelopeTypes)...)
			}
			offenders = append(offenders, handWrittenErrorStatuses(fset, fileRel, file)...)
		}
	})

	requireWalked(t, seen)

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("an HTTP error response must be written by problem.Write, "+
			"problem.WriteWithExtensions or problem.WriteCode, so every layer answers in the "+
			"one shape the SDK parses; these build one themselves:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// handBuiltEnvelopes reports error bodies assembled inside a function
// that answers an http.ResponseWriter: map literals keyed by a banned
// member, inline structs tagged with one, composite literals of a
// package type that is tagged with one, and JSON typed out by hand into
// a string literal.
func handBuiltEnvelopes(fset *token.FileSet, rel string, fn ast.Node, envelopeTypes map[string]bool) []string {
	var found []string
	at := func(n ast.Node) string {
		return fmt.Sprintf("%s:%d", rel, fset.Position(n.Pos()).Line)
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			switch lit := node.Type.(type) {
			case *ast.MapType:
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := stringLit(kv.Key); ok && bannedMembers[key] {
						found = append(found, at(node)+" builds an error body with a \""+key+"\" member")
					}
				}
			case *ast.StructType:
				if m, ok := bannedStructMember(lit); ok {
					found = append(found, at(node)+" builds an error body with a \""+m+"\" member")
				}
			case *ast.Ident:
				if envelopeTypes[lit.Name] {
					found = append(found, at(node)+" builds an error body from "+lit.Name)
				}
			}
		case *ast.BasicLit:
			if s, ok := stringLit(node); ok {
				for m := range bannedMembers {
					if strings.Contains(s, `"`+m+`":`) {
						found = append(found, at(node)+" writes a \""+m+"\" member as raw JSON")
					}
				}
			}
		}
		return true
	})
	return found
}

// handWrittenErrorStatuses reports calls that set an error status on the
// response directly. Catches an emitter that invents a fourth shape
// rather than reviving one of the old ones, which the member check
// above would not see.
//
// Methods named WriteHeader are skipped: those are ResponseWriter
// wrappers (status capture for logging and metrics) forwarding a status
// they were handed, not deciding one.
func handWrittenErrorStatuses(fset *token.FileSet, rel string, file *ast.File) []string {
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Recv != nil && fn.Name.Name == "WriteHeader" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteHeader" {
				return true
			}
			if why, bad := errorStatusArg(call.Args[0]); bad {
				found = append(found, fmt.Sprintf("%s:%d sets an error status by hand (%s)",
					rel, fset.Position(call.Pos()).Line, why))
			}
			return true
		})
	}
	return found
}

// errorStatusArg reports whether a WriteHeader argument names an error
// status. A plain variable is left alone — a helper that forwards
// whatever status it was given is judged by what it writes, which the
// member check covers.
func errorStatusArg(arg ast.Expr) (string, bool) {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind != token.INT {
			return "", false
		}
		code, err := strconv.Atoi(a.Value)
		if err != nil || code < 400 {
			return "", false
		}
		return a.Value, true
	case *ast.SelectorExpr:
		pkg, ok := a.X.(*ast.Ident)
		if ok && pkg.Name == "http" && strings.HasPrefix(a.Sel.Name, "Status") {
			if nonErrorStatuses[a.Sel.Name] {
				return "", false
			}
			return "http." + a.Sel.Name, true
		}
		// spec.Status and friends: a status read off an error catalog
		// entry is by definition an error status.
		if a.Sel.Name == "Status" {
			return exprName(a), true
		}
	}
	return "", false
}

// responderBodies returns the bodies of every function in the file that
// takes an http.ResponseWriter — the ones that can answer a request.
// Both plain functions and the closures middleware is built from count.
func responderBodies(file *ast.File) []ast.Node {
	var out []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil && takesResponseWriter(fn.Type) {
				out = append(out, fn.Body)
			}
		case *ast.FuncLit:
			if takesResponseWriter(fn.Type) {
				out = append(out, fn.Body)
			}
		}
		return true
	})
	return out
}

func takesResponseWriter(sig *ast.FuncType) bool {
	if sig.Params == nil {
		return false
	}
	for _, p := range sig.Params.List {
		if sel, ok := p.Type.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" && sel.Sel.Name == "ResponseWriter" {
				return true
			}
		}
	}
	return false
}

// structsWithBannedMembers collects the package's named struct types
// that carry a banned JSON member, so a responder that names one is
// caught even though the tags are declared elsewhere in the package —
// which is how the authentication middleware held its envelope.
func structsWithBannedMembers(files map[string]*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			if _, bad := bannedStructMember(st); bad {
				out[spec.Name.Name] = true
			}
			return true
		})
	}
	return out
}

// bannedStructMember reports the first banned JSON member a struct
// declares, if any.
func bannedStructMember(st *ast.StructType) (string, bool) {
	if st.Fields == nil {
		return "", false
	}
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		raw, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			continue
		}
		name, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
		if bannedMembers[name] {
			return name, true
		}
	}
	return "", false
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func exprName(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "?"
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		return id.Name + "." + sel.Sel.Name
	}
	return "." + sel.Sel.Name
}

func exempt(rel string) bool {
	for _, dir := range exemptDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// forEachPackage parses the non-test Go files of every directory under
// apps/ and packages/ and hands them to fn, keyed by the directory's
// slash-separated path relative to the repository root. Grouping is per
// directory because the struct declaration a responder uses may live in
// a sibling file.
func forEachPackage(t *testing.T, root string, fn func(rel string, fset *token.FileSet, files map[string]*ast.File)) {
	t.Helper()
	for _, tree := range []string{"apps", "packages"} {
		base := filepath.Join(root, tree)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			switch d.Name() {
			case "node_modules", "testdata", "generated", "dist":
				return filepath.SkipDir
			}
			entries, rerr := os.ReadDir(path)
			if rerr != nil {
				return rerr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			fset := token.NewFileSet()
			files := map[string]*ast.File{}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				// The walk root is this repository's own source tree,
				// fixed by the test rather than supplied by a caller.
				file, perr := parser.ParseFile(fset, filepath.Join(path, name), nil, 0) //#nosec G304 -- walk root is the repo source tree, fixed by the test
				if perr != nil {
					// A file that does not parse cannot compile either,
					// so the build already rejects it and this guard has
					// nothing to add.
					continue
				}
				files[filepath.ToSlash(filepath.Join(rel, name))] = file
			}
			if len(files) == 0 {
				return nil
			}
			fn(filepath.ToSlash(rel), fset, files)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
}

// walkSentinels are packages the guard must have inspected. "No file
// does X" is satisfied just as well by a walk that stopped finding
// files, so a layout change that quietly emptied the scan would leave
// this test green while checking nothing. One sentinel per module,
// because each is a separate Go module reached by its own path.
var walkSentinels = []string{
	"packages/go-shared/authn",
	"apps/flow-api/internal/http/handlers/handlerutil",
	"apps/auth-api/internal/http/handlers/handlerutil",
}

func requireWalked(t *testing.T, seen map[string]bool) {
	t.Helper()
	for _, want := range walkSentinels {
		if !seen[want] {
			t.Fatalf("the walk never reached %s, so it proved nothing; "+
				"the repository layout moved and this guard needs its sentinels updated", want)
		}
	}
}

// repoRoot returns the repository root. Tests run in the package
// directory, so it is three levels up from packages/go-shared/problem.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("expected the repository root at %s: %v", root, err)
	}
	return root
}
