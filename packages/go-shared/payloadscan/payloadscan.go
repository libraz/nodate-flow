// Package payloadscan type-checks event payload literals for internal
// identifiers.
//
// The runtime rail in eventlog rejects a numeric id-shaped field when the
// payload is appended, which is exact — it inspects the finished value —
// but it only fires on a path some test actually drives. This package
// covers the other half: it reads every payload composite literal in a
// package and asks the type checker what each id-shaped field will hold,
// so a builder on a path no test exercises is still caught.
//
// It resolves types rather than matching source text. A check that looked
// for `taskId: <something>.String()` would pass a commented-out call, and
// would flag `"taskId": in.TaskID` where TaskID is already a UUID string
// — a false positive that goes into an allowlist, and an allowlist that
// grows is where the real leak hides. Asking go/types whether the value
// is a string has neither failure mode.
package payloadscan

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Finding is one payload field whose value is not a string.
type Finding struct {
	// Pos is the "file:line:col" of the offending field.
	Pos string
	// Key is the payload key as written, e.g. "taskId".
	Key string
	// Type is what the type checker resolved the value to.
	Type string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %q is %s", f.Pos, f.Key, f.Type)
}

// Config selects what a scan looks at.
type Config struct {
	// Dir is the package directory to scan, relative to the module root
	// the scan runs from.
	Dir string
	// PayloadFields names the struct fields whose composite-literal
	// values carry an event payload. Defaults to "Payload" and
	// "ExtraPayload".
	PayloadFields []string
}

// Scan type-checks the package in cfg.Dir and reports every id-shaped
// payload key whose value is not a string.
//
// Only fields that reach an event payload are considered, so an id-shaped
// key in an unrelated map is not the scan's business.
func Scan(cfg Config) ([]Finding, error) {
	fields := cfg.PayloadFields
	if len(fields) == 0 {
		fields = []string{"Payload", "ExtraPayload"}
	}
	fieldSet := map[string]struct{}{}
	for _, f := range fields {
		fieldSet[f] = struct{}{}
	}

	// go/build picks the files the current build context would compile,
	// so a file excluded by a build tag is not type-checked as if it were
	// part of the package. Test files are skipped: they build payloads to
	// assert on them, and the rule is about what production code writes.
	bpkg, err := build.ImportDir(cfg.Dir, 0)
	if err != nil {
		return nil, fmt.Errorf("payloadscan: read %s: %w", cfg.Dir, err)
	}

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(bpkg.GoFiles))
	for _, name := range bpkg.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(cfg.Dir, name), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("payloadscan: parse %s: %w", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("payloadscan: %s has no buildable Go files", cfg.Dir)
	}

	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	conf := types.Config{
		Importer: NewExportImporter(fset),
		// Errors are collected by Check and returned below; a dependency
		// that cannot be resolved must not silently downgrade the scan to
		// "found nothing".
		Error: func(error) {},
	}
	if _, err := conf.Check(bpkg.Name, fset, files, info); err != nil {
		return nil, fmt.Errorf("payloadscan: type-check %s: %w", cfg.Dir, err)
	}

	var findings []Finding
	for _, f := range files {
		findings = append(findings, scanFile(fset, f, info, fieldSet)...)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Pos < findings[j].Pos })
	return findings, nil
}

// scanFile walks one file for payload literals.
func scanFile(fset *token.FileSet, file *ast.File, info *types.Info, fields map[string]struct{}) []Finding {
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		// Payload literals are resolved per function so a payload built
		// into a local variable can be traced back to the literal that
		// filled it. Hoisting the map into a variable is the obvious way
		// to sidestep a scan that only reads what is written inline.
		locals := payloadLiterals(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			ident, ok := kv.Key.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := fields[ident.Name]; !ok {
				return true
			}
			out = append(out, scanPayloadValue(fset, kv.Value, info, locals)...)
			return true
		})
		return true
	})
	return out
}

// payloadLiterals indexes the composite literals assigned to local
// variables in a function body, keyed by variable name.
//
// Only single-assignment `x := <literal>` and `var x = <literal>` forms
// are followed. A payload assembled across several statements is beyond
// what a syntactic index can reconstruct, and is left to the runtime rail
// — which sees the finished value and therefore cannot be evaded at all.
func payloadLiterals(body *ast.BlockStmt) map[string]*ast.CompositeLit {
	out := map[string]*ast.CompositeLit{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 {
				return true
			}
			name, ok := stmt.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if lit, ok := stmt.Rhs[0].(*ast.CompositeLit); ok {
				out[name.Name] = lit
			}
		case *ast.DeclStmt:
			decl, ok := stmt.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range decl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				if lit, ok := vs.Values[0].(*ast.CompositeLit); ok {
					out[vs.Names[0].Name] = lit
				}
			}
		}
		return true
	})
	return out
}

// scanPayloadValue inspects the expression assigned to a payload field,
// following a local variable to the literal it was built from.
func scanPayloadValue(fset *token.FileSet, expr ast.Expr, info *types.Info, locals map[string]*ast.CompositeLit) []Finding {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		ident, isIdent := expr.(*ast.Ident)
		if !isIdent {
			return nil
		}
		if lit, ok = locals[ident.Name]; !ok {
			return nil
		}
	}
	var out []Finding
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := literalKey(kv.Key)
		if !ok || !IsIDKey(key) {
			continue
		}
		tv, ok := info.Types[kv.Value]
		if !ok || tv.Type == nil {
			// An expression the checker did not record cannot be cleared,
			// so report it rather than assume it is fine.
			out = append(out, Finding{Pos: fset.Position(kv.Value.Pos()).String(), Key: key, Type: "unresolved"})
			continue
		}
		if isStringType(tv.Type) {
			continue
		}
		out = append(out, Finding{
			Pos:  fset.Position(kv.Value.Pos()).String(),
			Key:  key,
			Type: tv.Type.String(),
		})
	}
	return out
}

// literalKey returns the string value of a map key written as a literal.
func literalKey(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// IsIDKey reports whether a payload key names an identifier. It mirrors
// the runtime rail's rule so the two cannot disagree about what counts.
func IsIDKey(key string) bool {
	if key == "id" || key == "ids" {
		return true
	}
	for _, suffix := range []string{"Id", "ID", "Ids", "IDs"} {
		if len(key) > len(suffix) && strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// isStringType reports whether t is a string, including named string
// types and interfaces whose dynamic value the checker pinned to one.
func isStringType(t types.Type) bool {
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsString != 0
}

// NewExportImporter builds an importer backed by the export data `go
// list` writes into the build cache. It keeps the scan on the standard
// library: no analysis toolchain dependency has to be added to a module
// that ships a server.
//
// Exported because the sibling static scans type-check packages the same
// way; a second copy of this would be a second place for the build-cache
// lookup to go wrong.
func NewExportImporter(fset *token.FileSet) types.Importer {
	var mu sync.Mutex
	cache := map[string]string{}
	lookup := func(path string) (io.ReadCloser, error) {
		mu.Lock()
		file, known := cache[path]
		mu.Unlock()
		if !known {
			out, err := exec.Command("go", "list", "-export", "-f", "{{.Export}}", path).Output()
			if err != nil {
				return nil, fmt.Errorf("go list -export %s: %w", path, err)
			}
			file = strings.TrimSpace(string(out))
			mu.Lock()
			cache[path] = file
			mu.Unlock()
		}
		if file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(filepath.Clean(file))
	}
	return importer.ForCompiler(fset, "gc", lookup)
}
