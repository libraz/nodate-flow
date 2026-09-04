// Package outbounddeadline derives, from the committed Go, every place a
// request leaves this repository — and refuses one that can wait forever.
//
// An outbound call with no deadline is not a slow call. The goroutine that
// made it is held for as long as the peer keeps the socket open, and there
// is nothing to log and nothing to time out: no error is ever returned. Put
// one behind a lock and the failure stops being local, because every later
// caller queues behind it holding a connection of its own.
//
// net/http offers three ways to write one and none of them looks wrong:
//
//	http.DefaultClient    a package variable with no Timeout, so every
//	                      caller sharing it shares the absence.
//	http.Get / Post /     the package-level helpers, which are that same
//	Head / PostForm       variable spelled shorter.
//	http.Client{...}      a client constructed with every field somebody
//	                      needed and not the one nobody notices.
//
// The fourth way is the one that has no client in it at all. go-oidc and
// golang.org/x/oauth2 read their client out of the context and fall back to
// http.DefaultClient when it carries none, so `oidc.NewProvider(ctx, url)`
// and `cfg.Exchange(ctx, code)` are outbound calls with no deadline unless
// something upstream installed one. Nothing at the call site says so.
//
// So the check is in two halves:
//
//	construction    a client this repository builds or reaches for carries
//	                a Timeout.
//	installation    a call that reads its client out of a context is handed
//	                a context something installed one into.
//
// The second half is the one that reads dataflow, and it reads only as far
// as it can be sure: the context argument is either an install expression
// itself, or an identifier bound in the same function to one. An install is
// `oidc.ClientContext`, the `oauth2.HTTPClient` key, or a call to a function
// that transitively does one of those — derived across the whole tree, so
// naming a helper differently does not matter and deleting the install does.
//
// The scanned set is every hand-written Go file in the workspace. Tests are
// out: a hung request in a test is bounded by the test timeout and answers
// nobody, whereas the same call in a file the binaries build holds a login.
// A file that supports tests without being one is indistinguishable from
// product code by its name, so it stays in scope and says at the call why it
// is exempt — see [MarkerForm].
package outbounddeadline

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Import paths whose calls this package knows something about.
const (
	netHTTPPath = "net/http"
	oidcPath    = "github.com/coreos/go-oidc/v3/oidc"
	oauth2Path  = "golang.org/x/oauth2"
)

// MarkerForm is the machine-readable exemption, written in a comment above
// the site.
//
// The reason is mandatory. An exemption that states none is what the reader
// who finds it next has to reconstruct, and the two things it can mean —
// this call cannot hang, and nobody looked — are not distinguishable
// afterwards.
const MarkerForm = "outbound-deadline: not-applicable — <why this call cannot hang>"

// markerPattern matches [MarkerForm]. Requiring the reason to begin and end
// with a letter is what stops a sentence about the marker from acting as
// one, the rule the affected-rows and task-visibility gates use for theirs.
var markerPattern = regexp.MustCompile(
	`outbound-deadline:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)

// FindingKind is a way an outbound call ends up without a deadline.
type FindingKind int

const (
	// SharedDefaultClient is a use of http.DefaultClient, or of the
	// package-level helpers that are it under another name.
	SharedDefaultClient FindingKind = iota
	// ClientWithoutDeadline is an http.Client constructed with no Timeout.
	ClientWithoutDeadline
	// ContextWithoutClient is a call that reads its client out of the
	// context, handed a context nothing installed one into.
	ContextWithoutClient
	// StaleMarker is an exemption that covers no site.
	StaleMarker
)

// Finding is one site the check has something to say about.
type Finding struct {
	Kind FindingKind
	// File is repository-relative, Line the 1-based line.
	File string
	Line int
	// What names the expression, for the failure message.
	What string
	// Function is the enclosing function, empty at package level.
	Function string
	// Marked records that an exemption was paired to this site.
	Marked bool
	// pos orders findings and markers against each other.
	pos token.Pos
}

// Location renders the site's position for a failure message.
func (f Finding) Location() string { return fmt.Sprintf("%s:%d", f.File, f.Line) }

// Scan is what one pass over the tree found, kept together because the way
// a derived check fails is by deriving nothing and then passing.
type Scan struct {
	// Files is how many files were parsed.
	Files int
	// Findings are the sites, exempted or not, plus the stale markers.
	Findings []Finding
	// Clients counts the http.Client literals seen, deadline or not, and
	// ContextCalls the calls that read a client out of a context. Both are
	// the halves of the check having matched anything at all.
	Clients      int
	ContextCalls int
	// Installers are the functions that put a client into a context,
	// directly or through another one.
	Installers []string
	// Markers counts the exemptions found, paired or not.
	Markers int
}

// Unexempted returns the findings nothing excused.
func (s Scan) Unexempted() []Finding {
	var out []Finding
	for _, f := range s.Findings {
		if !f.Marked {
			out = append(out, f)
		}
	}
	return out
}

// ----------------------------------------------------------------------
// Repository access
// ----------------------------------------------------------------------

// RepoRoot returns the repository root, found by walking up from the
// caller's working directory to the go.work that defines the workspace.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("outbounddeadline: go.work not found above the working directory")
		}
		dir = parent
	}
}

// skippedDirs are the trees that hold no hand-written outbound call:
// vendored packages, build output, and generated code.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"backup":       true,
	"dist":         true,
	"bin":          true,
	"generated":    true,
}

// SourceFiles returns every hand-written Go file in the workspace,
// repository-relative and slash-separated.
func SourceFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if skippedDirs[name] || (strings.HasPrefix(name, ".") && path != root) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// ScanRepository parses every hand-written Go file under root and holds
// each outbound call to a deadline.
func ScanRepository(root string) (Scan, error) {
	names, err := SourceFiles(root)
	if err != nil {
		return Scan{}, err
	}
	fset := token.NewFileSet()
	sources := make([]Source, 0, len(names))
	for _, name := range names {
		file, parseErr := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.ParseComments)
		if parseErr != nil {
			return Scan{}, fmt.Errorf("parse %s: %w", name, parseErr)
		}
		sources = append(sources, Source{Name: name, File: file})
	}
	return ScanSources(fset, sources), nil
}

// Source is one parsed file under its repository-relative name.
type Source struct {
	Name string
	File *ast.File
}

// ScanSources holds a parsed set of files to the rule. It is the entry
// point the control uses, so the control exercises the same code the
// repository scan does.
func ScanSources(fset *token.FileSet, sources []Source) Scan {
	installers := Installers(sources)

	scan := Scan{Files: len(sources), Installers: sortedKeys(installers)}
	for _, src := range sources {
		found, clients, contextCalls, markers := scanFile(fset, src, installers)
		scan.Findings = append(scan.Findings, found...)
		scan.Clients += clients
		scan.ContextCalls += contextCalls
		scan.Markers += markers
	}
	return scan
}

// ----------------------------------------------------------------------
// Installers
// ----------------------------------------------------------------------

// Installers returns the names of the functions that put an HTTP client
// into a context, directly or by calling another one that does.
//
// The set is derived rather than named. A helper renamed tomorrow is still
// an installer; a helper whose body stops installing stops being one, and
// every call site that relied on it fails at once.
//
// Functions are keyed by their own name, not by package: the install is a
// property of the body, and the call sites reach it through receivers and
// package qualifiers this check deliberately does not resolve.
//
// Only a function returning a context.Context can be one. That is what
// keeps the name-keyed closure honest — without it a function called Do
// that happens to install somewhere inside itself would excuse every call
// named Do in the tree, and a helper that installs into a field of its own
// hands its caller nothing anyway.
func Installers(sources []Source) map[string]bool {
	direct := map[string]bool{}
	calls := map[string][]string{}

	for _, src := range sources {
		im := importsOf(src.File)
		for _, decl := range src.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !returnsContext(fn, im) {
				continue
			}
			name := fn.Name.Name
			if installsDirectly(fn.Body, im) {
				direct[name] = true
			}
			calls[name] = append(calls[name], calleeNames(fn.Body)...)
		}
	}

	// Close over the call graph: a function that calls an installer is one.
	for changed := true; changed; {
		changed = false
		for name, callees := range calls {
			if direct[name] {
				continue
			}
			for _, callee := range callees {
				if direct[callee] {
					direct[name] = true
					changed = true
					break
				}
			}
		}
	}
	return direct
}

// installsDirectly reports whether a body puts a client into a context
// itself, by either spelling the libraries accept.
func installsDirectly(body ast.Node, im imports) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch {
		case im.qualifies(sel.X, oidcPath) && sel.Sel.Name == "ClientContext":
			found = true
		case im.qualifies(sel.X, oauth2Path) && sel.Sel.Name == "HTTPClient":
			found = true
		}
		return !found
	})
	return found
}

// returnsContext reports whether a function hands a context back to its
// caller, which is the only way an install of its own can reach one.
func returnsContext(fn *ast.FuncDecl, im imports) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, result := range fn.Type.Results.List {
		sel, ok := result.Type.(*ast.SelectorExpr)
		if ok && im.qualifies(sel.X, "context") && sel.Sel.Name == "Context" {
			return true
		}
	}
	return false
}

// calleeNames returns the bare name of every function called in a body,
// whether through a receiver, a package qualifier or neither.
func calleeNames(body ast.Node) []string {
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			out = append(out, fn.Name)
		case *ast.SelectorExpr:
			out = append(out, fn.Sel.Name)
		}
		return true
	})
	return out
}

// ----------------------------------------------------------------------
// Calls that read their client out of the context
// ----------------------------------------------------------------------

// qualifiedContextCalls are package-level functions taking a context whose
// HTTP client they read out of it. Every one of them takes the context
// first.
var qualifiedContextCalls = map[string][]string{
	oidcPath:   {"NewProvider", "NewRemoteKeySet"},
	oauth2Path: {"NewClient"},
}

// methodContextCalls are the methods on go-oidc and oauth2 values that do
// the same.
//
// The receiver's type is not resolved: a check keyed on "c.oauth" would be
// satisfied by renaming the field, and the value reaches these calls
// through wrappers and embedded structs besides. The name counts in any
// file importing the library it belongs to, which is the narrowest scope
// that does not depend on how the value was stored.
var methodContextCalls = map[string][]string{
	oauth2Path: {"Exchange", "Client", "TokenSource", "PasswordCredentialsToken", "DeviceAccessToken"},
	oidcPath:   {"Verify", "UserInfo"},
}

// contextCall reports the name of the library call this expression makes,
// and whether it makes one.
func contextCall(call *ast.CallExpr, im imports) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	for path, names := range qualifiedContextCalls {
		if !im.qualifies(sel.X, path) {
			continue
		}
		for _, name := range names {
			if sel.Sel.Name == name {
				return short(path) + "." + name, true
			}
		}
	}
	for path, names := range methodContextCalls {
		if !im.uses(path) {
			continue
		}
		// A package-qualified call to a name in this set belongs to
		// whichever package qualifies it, and it was either matched above
		// or is not one of these at all.
		if ident, isIdent := sel.X.(*ast.Ident); isIdent && im.binds(ident.Name) {
			continue
		}
		for _, name := range names {
			if sel.Sel.Name == name {
				return name, true
			}
		}
	}
	return "", false
}

// ----------------------------------------------------------------------
// The file scan
// ----------------------------------------------------------------------

func scanFile(fset *token.FileSet, src Source, installers map[string]bool) (
	findings []Finding, clients, contextCalls, markers int,
) {
	im := importsOf(src.File)

	// Identifiers bound to an install, per enclosing function. A context
	// assigned from an install expression carries the client wherever it
	// is used afterwards.
	installedIn := map[*ast.FuncDecl]map[string]bool{}
	enclosing := func(pos token.Pos) *ast.FuncDecl {
		for _, decl := range src.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && pos >= fn.Pos() && pos <= fn.End() {
				return fn
			}
		}
		return nil
	}
	for _, decl := range src.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			installedIn[fn] = installedIdents(fn.Body, im, installers)
		}
	}

	add := func(kind FindingKind, pos token.Pos, what string) {
		fn := ""
		if decl := enclosing(pos); decl != nil {
			fn = decl.Name.Name
		}
		findings = append(findings, Finding{
			Kind:     kind,
			File:     src.Name,
			Line:     fset.Position(pos).Line,
			What:     what,
			Function: fn,
			pos:      pos,
		})
	}

	ast.Inspect(src.File, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if im.qualifies(node.X, netHTTPPath) && node.Sel.Name == "DefaultClient" {
				add(SharedDefaultClient, node.Pos(), "http.DefaultClient")
			}
		case *ast.CompositeLit:
			if !isHTTPClientType(node.Type, im) {
				return true
			}
			clients++
			if !hasDeadline(node) {
				add(ClientWithoutDeadline, node.Pos(), "http.Client")
			}
		case *ast.CallExpr:
			if name, ok := defaultClientHelper(node, im); ok {
				add(SharedDefaultClient, node.Pos(), name)
				return true
			}
			name, ok := contextCall(node, im)
			if !ok {
				return true
			}
			contextCalls++
			if len(node.Args) == 0 {
				return true
			}
			fn := enclosing(node.Pos())
			if carriesClient(node.Args[0], im, installers, installedIn[fn]) {
				return true
			}
			add(ContextWithoutClient, node.Pos(), name)
		}
		return true
	})

	sort.SliceStable(findings, func(i, j int) bool { return findings[i].pos < findings[j].pos })
	findings, stale, markers := pairMarkers(fset, src, findings)
	return append(findings, stale...), clients, contextCalls, markers
}

// pairMarkers attaches each exemption to the first unexempted site after
// it, and reports the ones left over.
//
// Position order is what pairs them, so the exemption sits above the site
// it excuses and cannot outlive it: delete the call and the marker becomes
// stale, which is reported rather than ignored.
func pairMarkers(fset *token.FileSet, src Source, findings []Finding) (
	paired []Finding, stale []Finding, count int,
) {
	var positions []token.Pos
	for _, group := range src.File.Comments {
		for _, c := range group.List {
			if markerPattern.MatchString(c.Text) {
				positions = append(positions, c.Pos())
			}
		}
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i] < positions[j] })

	next := 0
	for i := range findings {
		if next < len(positions) && positions[next] < findings[i].pos {
			findings[i].Marked = true
			next++
		}
	}
	for ; next < len(positions); next++ {
		stale = append(stale, Finding{
			Kind: StaleMarker,
			File: src.Name,
			Line: fset.Position(positions[next]).Line,
			pos:  positions[next],
		})
	}
	return findings, stale, len(positions)
}

// installedIdents returns the identifiers a function body binds to an
// expression that installs a client.
func installedIdents(body *ast.BlockStmt, im imports, installers map[string]bool) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		installs := false
		for _, rhs := range assign.Rhs {
			if installExpr(rhs, im, installers) {
				installs = true
			}
		}
		if !installs {
			return true
		}
		for _, lhs := range assign.Lhs {
			if ident, isIdent := lhs.(*ast.Ident); isIdent && ident.Name != "_" {
				out[ident.Name] = true
			}
		}
		return true
	})
	return out
}

// carriesClient reports whether the context handed to a library call had a
// client put into it.
func carriesClient(arg ast.Expr, im imports, installers, bound map[string]bool) bool {
	if installExpr(arg, im, installers) {
		return true
	}
	ident, ok := arg.(*ast.Ident)
	return ok && bound[ident.Name]
}

// installExpr reports whether evaluating an expression installs a client:
// the library call that does it, the context key it uses, or a call to a
// function that reaches one of those.
func installExpr(expr ast.Expr, im imports, installers map[string]bool) bool {
	if installsDirectly(expr, im) {
		return true
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			found = found || installers[fn.Name]
		case *ast.SelectorExpr:
			found = found || installers[fn.Sel.Name]
		}
		return !found
	})
	return found
}

// ----------------------------------------------------------------------
// net/http shapes
// ----------------------------------------------------------------------

// defaultClientHelpers are the package-level functions that run on
// http.DefaultClient.
var defaultClientHelpers = []string{"Get", "Head", "Post", "PostForm"}

func defaultClientHelper(call *ast.CallExpr, im imports) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !im.qualifies(sel.X, netHTTPPath) {
		return "", false
	}
	for _, name := range defaultClientHelpers {
		if sel.Sel.Name == name {
			return "http." + name, true
		}
	}
	return "", false
}

// isHTTPClientType reports whether a composite literal builds an
// http.Client, addressed or not.
func isHTTPClientType(expr ast.Expr, im imports) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && im.qualifies(sel.X, netHTTPPath) && sel.Sel.Name == "Client"
}

// hasDeadline reports whether a client literal sets a Timeout to anything
// but zero. `Timeout: 0` is the field written and the deadline still
// absent, which reads as considered and is not.
func hasDeadline(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Timeout" {
			continue
		}
		if basic, isBasic := kv.Value.(*ast.BasicLit); isBasic && basic.Value == "0" {
			return false
		}
		return true
	}
	return false
}

// ----------------------------------------------------------------------
// Imports
// ----------------------------------------------------------------------

// imports maps the identifier a file binds an import to onto its path.
type imports map[string]string

func importsOf(file *ast.File) imports {
	out := imports{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		out[name] = path
	}
	return out
}

// qualifies reports whether an expression is the identifier this file
// bound the given import path to.
func (im imports) qualifies(expr ast.Expr, path string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && im[ident.Name] == path
}

// uses reports whether the file imports the path at all.
func (im imports) uses(path string) bool {
	for _, p := range im {
		if p == path {
			return true
		}
	}
	return false
}

// binds reports whether a name is one of the file's imports.
func (im imports) binds(name string) bool {
	_, ok := im[name]
	return ok
}

func short(path string) string { return path[strings.LastIndex(path, "/")+1:] }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
