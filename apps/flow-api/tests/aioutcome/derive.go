package aioutcome

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Roots are the directory trees scanned, repository-relative. Every call
// into an LLM provider is made from this tree, so it is the whole scope
// in which the outcome of one can go unrecorded.
var Roots = []string{"apps/flow-api/internal/ai"}

// excludedTrees are repository-relative directories left out of the
// scan. The providers tree implements the provider interface rather than
// calling it: its own Complete and Embed methods are the callee at every
// site this checks, not a call site of their own.
var excludedTrees = map[string]bool{
	"apps/flow-api/internal/ai/providers": true,
}

// skippedDirs are trees a Go parse has no business entering.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
}

// providerMethods are the methods that spend an invocation. A call to
// one of these has already cost tokens and latency by the time it
// returns, on either path.
var providerMethods = map[string]bool{
	"Complete": true,
	"Embed":    true,
}

// hookName is the invocation metrics hook. A call to a value of this
// name is what records an invocation; everything else reaches it by
// calling something that does.
const hookName = "OnInvocation"

// Kind is what is wrong with one provider call.
type Kind string

// The kinds a finding can have. Each names a way the counters end up
// disagreeing with the calls that were actually made.
const (
	// KindUnchecked is a provider call whose error is discarded or not
	// branched on at all, so neither path is distinguishable.
	KindUnchecked Kind = "unchecked"
	// KindFailureUnrecorded is a provider call that records on success
	// and returns silently on failure.
	KindFailureUnrecorded Kind = "failure-unrecorded"
	// KindSuccessUnrecorded is a provider call that records on failure
	// and returns silently on success.
	KindSuccessUnrecorded Kind = "success-unrecorded"
	// KindNeitherRecorded is a provider call neither of whose branches
	// reaches the hook.
	KindNeitherRecorded Kind = "neither-recorded"
	// KindPathUnrecorded is a provider call one of whose two branches
	// records nothing, where the check does not say which branch is
	// which.
	KindPathUnrecorded Kind = "path-unrecorded"
	// KindMislabel is a hook-reaching call passing a literal nil in its
	// trailing error position from inside an error check.
	KindMislabel Kind = "mislabel"
)

// Finding is one provider call whose outcome the counters will not
// agree with.
type Finding struct {
	File  string
	Line  int
	Kind  Kind
	Chain []string
}

// Location renders the finding's position for a failure message.
func (f Finding) Location() string { return fmt.Sprintf("%s:%d", f.File, f.Line) }

// ChainString renders the route from the call to the invocation hook,
// which is what says where the fix belongs: for an omission it is the
// route the surviving path takes and the silent path does not, and for a
// mislabel it is the hop that will be handed the nil.
func (f Finding) ChainString() string { return strings.Join(f.Chain, " -> ") }

// String renders the finding as position and kind, which is the form
// worth comparing a scan against.
func (f Finding) String() string { return fmt.Sprintf("%s: %s", f.Location(), f.Kind) }

// Explain states what the finding costs and what closes it.
func (f Finding) Explain() string {
	switch f.Kind {
	case KindUnchecked:
		return "this provider call spends an invocation and then goes on without " +
			"telling its two outcomes apart, so nothing can record which one it was. " +
			"Bind the error and branch on it"
	case KindFailureUnrecorded:
		return "this provider call records its success and returns silently on failure, " +
			"so a call that was made and failed is counted as no call at all. The metric " +
			"reads lower, which looks like less traffic rather than like an error. Notify " +
			"the invocation hook on the failure path too, passing the provider's error"
	case KindSuccessUnrecorded:
		return "this provider call records its failure and returns silently on success, " +
			"so the counters describe only the calls that went wrong and every rate derived " +
			"from them reads as total failure. Notify the invocation hook on the success " +
			"path too, passing no error"
	case KindNeitherRecorded:
		return "this provider call is made and neither of its paths notifies the invocation " +
			"hook, so the call is invisible in the metric and in ai_invocations however it " +
			"went. Notify the hook on both paths, passing the provider's error on the one " +
			"that failed and no error on the one that succeeded"
	case KindPathUnrecorded:
		return "one of this provider call's two branches records nothing, so an invocation " +
			"that took it is counted as no call at all. Which branch runs on the failure " +
			"could not be read off the condition, which compares the error against nil more " +
			"than one way round, so the missing side is not named here. Notify the invocation " +
			"hook from both branches"
	case KindMislabel:
		return "this call sits inside an error check and passes a literal nil where the " +
			"invocation's error goes, so a failure is counted as a success. That is worse " +
			"than counting nothing: the error rate reads as zero rather than as low. Pass " +
			"the error being checked"
	}
	return string(f.Kind)
}

// RootCounts is what one root yielded. A root that yielded nothing has
// reached no verdict, so these are checked rather than only printed.
type RootCounts struct {
	Root          string
	GoFiles       int
	ProviderCalls int
	HookFuncs     int
}

// String renders the counts for the run's output.
func (c RootCounts) String() string {
	return fmt.Sprintf("%s: %d Go files, %d provider calls, %d hook-reaching functions",
		c.Root, c.GoFiles, c.ProviderCalls, c.HookFuncs)
}

// Result is one scan: what was found, and what was looked at to find it.
type Result struct {
	Findings []Finding
	Counts   []RootCounts
}

// CheckNonEmpty reports the first root that yielded nothing. A scan that
// parsed no files, matched no provider call, or derived no hook-reaching
// function has not cleared the tree — it has failed to read it, and must
// never be reported as a pass.
func (r Result) CheckNonEmpty() error {
	if len(r.Counts) == 0 {
		return errors.New("aioutcome: no root was scanned at all")
	}
	for _, c := range r.Counts {
		switch {
		case c.GoFiles == 0:
			return fmt.Errorf("aioutcome: %s yielded no .go file; the walk has stopped matching "+
				"rather than the sources having gone away", c.Root)
		case c.ProviderCalls == 0:
			return fmt.Errorf("aioutcome: %s yielded no provider call; an AI tree that calls "+
				"neither Complete nor Embed anywhere means the match has stopped recognising "+
				"the call, and the check ranged over nothing", c.Root)
		case c.HookFuncs == 0:
			return fmt.Errorf("aioutcome: %s yielded no function that reaches %s; with an empty "+
				"reach set every call site looks unrecorded and none of them would be believed",
				c.Root, hookName)
		}
	}
	return nil
}

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
			return "", errors.New("aioutcome: go.work not found above the working directory")
		}
		dir = parent
	}
}

// File is one parsed source file, kept with the repository-relative path
// a failure message has to name.
type File struct {
	Rel string
	AST *ast.File
}

// Package is every Go file in one directory. A directory is the unit
// because that is the scope in which an unqualified call resolves: a
// call site reaches a recording helper written beside it without naming
// a package.
type Package struct {
	Dir        string
	ImportPath string
	Files      []File
}

// Scan parses every Go file under the given roots and returns the
// provider calls whose outcome nothing records, together with what each
// root yielded.
func Scan(root string, roots []string) (Result, error) {
	var result Result
	fset := token.NewFileSet()
	var all []Package
	for _, rel := range roots {
		pkgs, err := parseRoot(fset, root, rel)
		if err != nil {
			return Result{}, err
		}
		counts := RootCounts{Root: rel}
		for _, pkg := range pkgs {
			counts.GoFiles += len(pkg.Files)
		}
		result.Counts = append(result.Counts, counts)
		all = append(all, pkgs...)
	}

	findings, reach, callsByDir := analyze(fset, all)
	result.Findings = findings

	// Attribute the derived totals back to the root each package came
	// from, so a root that contributed nothing is visible as such rather
	// than hidden behind another root's numbers.
	for i, c := range result.Counts {
		prefix := c.Root + string(filepath.Separator)
		for key := range reach {
			if strings.HasPrefix(key.dir, prefix) || key.dir == c.Root {
				result.Counts[i].HookFuncs++
			}
		}
		for dir, n := range callsByDir {
			if strings.HasPrefix(dir, prefix) || dir == c.Root {
				result.Counts[i].ProviderCalls += n
			}
		}
	}
	return result, nil
}

// parseRoot parses every Go file under one root, grouped by directory.
func parseRoot(fset *token.FileSet, root, rel string) ([]Package, error) {
	dir := filepath.Join(root, rel)
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	byDir := map[string]*Package{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)
		if entry.IsDir() {
			name := entry.Name()
			if path != dir && (skippedDirs[name] || excludedTrees[relPath] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		// Test files are left out of the walk, the counts included. A
		// test that calls a provider is exercising the provider rather
		// than spending a workspace's budget, and it has no invocation
		// hook behind it to notify.
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(fset, relPath, src, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", relPath, parseErr)
		}
		dirRel := filepath.Dir(relPath)
		pkg := byDir[dirRel]
		if pkg == nil {
			pkg = &Package{Dir: dirRel}
			byDir[dirRel] = pkg
		}
		pkg.Files = append(pkg.Files, File{Rel: relPath, AST: parsed})
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]Package, 0, len(byDir))
	for _, pkg := range byDir {
		sort.Slice(pkg.Files, func(i, j int) bool { return pkg.Files[i].Rel < pkg.Files[j].Rel })
		pkg.ImportPath = importPath(root, pkg.Dir)
		out = append(out, *pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// importPath resolves a directory to the path other packages import it
// by, so a call site recording through a helper in a sibling package of
// this tree is followed rather than assumed silent. A directory under no
// module resolves to nothing and is reachable only from beside it.
func importPath(root, dirRel string) string {
	dir := dirRel
	for {
		modPath := moduleName(filepath.Join(root, dir, "go.mod"))
		if modPath != "" {
			suffix, err := filepath.Rel(dir, dirRel)
			if err != nil || suffix == "." {
				return modPath
			}
			return modPath + "/" + filepath.ToSlash(suffix)
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." {
			return ""
		}
		dir = parent
	}
}

// moduleName reads the module path from a go.mod, or "" when the file is
// not there.
func moduleName(path string) string {
	f, err := os.Open(path) //#nosec G304,G122 -- repository path walked at test time
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// funcKey identifies one function by the directory it is declared in and
// its name. Methods are keyed by name too: two types sharing a method
// name is rare, and merging them can only widen the reach set, which
// errs towards believing a call site records.
type funcKey struct {
	dir  string
	name string
}

// reachStep records why a function reaches the hook: either it calls the
// hook value directly, or it calls something that does.
type reachStep struct {
	primitive string
	next      funcKey
	hasNext   bool
}

// analyze derives the hook-reaching functions across the given packages
// and returns the provider calls whose outcome they do not account for,
// along with how many provider calls each directory holds.
func analyze(fset *token.FileSet, pkgs []Package) ([]Finding, map[funcKey]reachStep, map[string]int) {
	byImportPath := map[string]string{}
	for _, pkg := range pkgs {
		if pkg.ImportPath != "" {
			byImportPath[pkg.ImportPath] = pkg.Dir
		}
	}

	// Collect every function declaration, with the calls its body makes
	// resolved to the directory they land in.
	type funcBody struct {
		key   funcKey
		prims []string
		calls []funcKey
	}
	var bodies []funcBody
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			imports := importAliases(f.AST, byImportPath)
			for _, decl := range f.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				prims, calls := scanBody(fn.Body, pkg.Dir, imports)
				bodies = append(bodies, funcBody{key: funcKey{dir: pkg.Dir, name: fn.Name.Name}, prims: prims, calls: calls})
			}
		}
	}

	// Fixpoint: a function reaches the hook if it calls the hook value,
	// or calls something that reaches. Iterating until nothing changes is
	// the whole point — a one-level walk reports a call site that records
	// through two hops of helper as recording nothing, and a checker that
	// cries wolf on correct code is removed rather than fixed.
	reach := map[funcKey]reachStep{}
	for _, b := range bodies {
		if len(b.prims) > 0 {
			reach[b.key] = reachStep{primitive: b.prims[0]}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range bodies {
			if _, done := reach[b.key]; done {
				continue
			}
			for _, callee := range b.calls {
				if _, ok := reach[callee]; ok {
					reach[b.key] = reachStep{next: callee, hasNext: true}
					changed = true
					break
				}
			}
		}
	}

	var findings []Finding
	callsByDir := map[string]int{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			imports := importAliases(f.AST, byImportPath)
			for _, decl := range f.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ctx := &walkContext{
					fset:    fset,
					file:    f.Rel,
					dir:     pkg.Dir,
					imports: imports,
					reach:   reach,
					errs:    errorIdents(fn),
				}
				findings = append(findings, ctx.omissions(fn.Body)...)
				findings = append(findings, ctx.mislabels(fn.Body)...)
				callsByDir[pkg.Dir] += ctx.providerCalls
			}
		}
	}
	return dedupe(findings), reach, callsByDir
}

// walkContext carries what one function declaration is judged against.
type walkContext struct {
	fset          *token.FileSet
	file          string
	dir           string
	imports       map[string]string
	reach         map[funcKey]reachStep
	errs          map[string]bool
	providerCalls int
}

// omissions returns the provider calls in one function body that record
// on fewer than both of their paths, and counts every provider call it
// passed on the way.
func (c *walkContext) omissions(body *ast.BlockStmt) []Finding {
	var out []Finding
	for _, list := range statementLists(body) {
		for i, stmt := range list {
			assign, ok := stmt.(*ast.AssignStmt)
			if !ok {
				continue
			}
			errIdent, ok := providerCall(assign)
			if !ok {
				continue
			}
			c.providerCalls++
			at := func(kind Kind, chain []string) Finding {
				return Finding{
					File:  c.file,
					Line:  c.fset.Position(assign.Pos()).Line,
					Kind:  kind,
					Chain: chain,
				}
			}
			if errIdent == "" || errIdent == "_" {
				out = append(out, at(KindUnchecked, nil))
				continue
			}
			check, checkAt := findErrorCheck(list, i+1, errIdent)
			if check == nil {
				out = append(out, at(KindUnchecked, nil))
				continue
			}
			sense := errorCheckSense(check.Cond, errIdent)

			inBody := c.firstReaching(check.Body)
			// The other branch is whatever runs when the if-body does
			// not: an else when the code is written that way round, and
			// otherwise the statements the check falls through to. What
			// ran before the check is neither branch — it ran whatever
			// the outcome was, so a hook call there records nothing
			// about which one it was.
			other := c.firstReachingIn(list[checkAt+1:])
			if other == nil && check.Else != nil {
				other = c.firstReaching(check.Else)
			}

			// Both branches silent is one fault, not two. Reported as a
			// pair it reads as a contradiction — the same line said to
			// record its success and to record its failure — and the
			// reader has to reconcile the two before arriving at the
			// plain answer, which is that nothing records anything.
			if inBody == nil && other == nil {
				out = append(out, at(KindNeitherRecorded, nil))
				continue
			}

			switch sense {
			case senseFailureInBody:
				if inBody == nil {
					out = append(out, at(KindFailureUnrecorded, other))
				}
				if other == nil {
					out = append(out, at(KindSuccessUnrecorded, inBody))
				}
			case senseSuccessInBody:
				if inBody == nil {
					out = append(out, at(KindSuccessUnrecorded, other))
				}
				if other == nil {
					out = append(out, at(KindFailureUnrecorded, inBody))
				}
			case senseAmbiguous:
				// One branch records and the other does not, and the
				// condition does not say which is which. Both recording
				// needs no verdict at all: whichever way round it reads,
				// neither outcome is lost.
				if inBody == nil || other == nil {
					out = append(out, at(KindPathUnrecorded, firstChain(inBody, other)))
				}
			case senseNone:
			}
		}
	}
	return out
}

// mislabels returns the hook-reaching calls in one function body that
// pass a literal nil in their trailing error position from inside an
// error check. The rule stops at the call: what the callee does with the
// value is that callee's own call sites' problem, so a helper forwarding
// its error parameter is not a finding.
func (c *walkContext) mislabels(body *ast.BlockStmt) []Finding {
	var out []Finding
	ast.Inspect(body, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok || !c.isErrorCheck(ifStmt.Cond) {
			return true
		}
		ast.Inspect(ifStmt.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			if !isNilLiteral(call.Args[len(call.Args)-1]) {
				return true
			}
			if chain := c.chainTo(call); chain != nil {
				out = append(out, Finding{
					File:  c.file,
					Line:  c.fset.Position(call.Pos()).Line,
					Kind:  KindMislabel,
					Chain: chain,
				})
			}
			return true
		})
		return true
	})
	return out
}

// firstReaching returns the route to the hook taken by the first call in
// a subtree that reaches it, or nil when none does.
func (c *walkContext) firstReaching(node ast.Node) []string {
	var chain []string
	ast.Inspect(node, func(n ast.Node) bool {
		if chain != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if found := c.chainTo(call); found != nil {
			chain = found
			return false
		}
		return true
	})
	return chain
}

// firstReachingIn is firstReaching over a run of statements.
func (c *walkContext) firstReachingIn(stmts []ast.Stmt) []string {
	for _, stmt := range stmts {
		if chain := c.firstReaching(stmt); chain != nil {
			return chain
		}
	}
	return nil
}

// chainTo returns the route from one call to the hook, or nil when it
// cannot reach it.
func (c *walkContext) chainTo(call *ast.CallExpr) []string {
	if prim, ok := hookPrimitive(call); ok {
		return []string{prim}
	}
	key, name, ok := resolveCall(call, c.dir, c.imports)
	if !ok {
		return nil
	}
	if _, reaches := c.reach[key]; !reaches {
		return nil
	}
	return append([]string{name}, chainFrom(key, c.reach)[1:]...)
}

// isErrorCheck reports whether a condition is a nil check on an
// identifier known to hold an error. Anything else compared against nil
// is left alone: a branch taken because a cached value is present reads
// identically, and it records a success that really happened.
func (c *walkContext) isErrorCheck(cond ast.Expr) bool {
	ident, ok := nilCheckIdent(cond)
	return ok && c.errs[ident]
}

// chainFrom follows the reach map from one function to the hook it ends
// at.
func chainFrom(key funcKey, reach map[funcKey]reachStep) []string {
	chain := []string{key.name}
	seen := map[funcKey]bool{key: true}
	for {
		step, ok := reach[key]
		if !ok {
			return chain
		}
		if !step.hasNext {
			return append(chain, step.primitive)
		}
		key = step.next
		if seen[key] {
			// Mutual recursion between two reaching functions; the chain
			// already names the route worth reading.
			return chain
		}
		seen[key] = true
		chain = append(chain, key.name)
	}
}

// scanBody returns the hook values a body calls directly and the
// functions it calls that might reach one. Function literals inside the
// body count as part of it: a closure defined here records for this
// function.
func scanBody(body *ast.BlockStmt, dir string, imports map[string]string) (prims []string, calls []funcKey) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if prim, ok := hookPrimitive(call); ok {
			prims = append(prims, prim)
			return true
		}
		if key, _, ok := resolveCall(call, dir, imports); ok {
			calls = append(calls, key)
		}
		return true
	})
	return prims, calls
}

// hookPrimitive reports whether a call notifies the invocation hook
// itself, and renders the value it was called through.
func hookPrimitive(call *ast.CallExpr) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == hookName {
			return hookName, true
		}
	case *ast.SelectorExpr:
		if fun.Sel.Name == hookName {
			return exprName(fun), true
		}
	}
	return "", false
}

// resolveCall maps a call to the function it names, within this
// directory or in another scanned package.
func resolveCall(call *ast.CallExpr, dir string, imports map[string]string) (funcKey, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return funcKey{dir: dir, name: fun.Name}, fun.Name, true
	case *ast.SelectorExpr:
		pkgIdent, ok := fun.X.(*ast.Ident)
		if !ok {
			// A method on a field, such as s.metrics.Record: the
			// receiver's package is unknown without type information, so
			// the method is matched by name within this directory.
			return funcKey{dir: dir, name: fun.Sel.Name}, fun.Sel.Name, true
		}
		if otherDir, ok := imports[pkgIdent.Name]; ok {
			return funcKey{dir: otherDir, name: fun.Sel.Name},
				pkgIdent.Name + "." + fun.Sel.Name, true
		}
		return funcKey{dir: dir, name: fun.Sel.Name}, fun.Sel.Name, true
	}
	return funcKey{}, "", false
}

// importAliases maps the name a file refers to an imported package by
// onto the directory that package was parsed from. Imports outside the
// scanned roots have no entry and are left alone.
func importAliases(file *ast.File, byImportPath map[string]string) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		dir, ok := byImportPath[path]
		if !ok {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = dir
	}
	return out
}

// providerCall reports whether an assignment is a call into a provider,
// and names the identifier its error lands in. The trailing destination
// is the error by Go's own convention, and it is what the check that
// follows has to be written against.
func providerCall(assign *ast.AssignStmt) (string, bool) {
	if len(assign.Rhs) != 1 || len(assign.Lhs) < 2 {
		return "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !providerMethods[sel.Sel.Name] {
		return "", false
	}
	ident, ok := assign.Lhs[len(assign.Lhs)-1].(*ast.Ident)
	if !ok {
		return "", true
	}
	return ident.Name, true
}

// errorIdents returns the identifiers inside a declaration that hold an
// error, derived without type information: a name declared error in a
// signature or a var declaration, and the trailing destination of a
// multi-value assignment from a call, which is where Go puts one. A name
// assigned any other way is not assumed to be an error, because a nil
// check on it is then not an error check.
func errorIdents(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	collect := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if ident, ok := field.Type.(*ast.Ident); !ok || ident.Name != "error" {
				continue
			}
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
	}
	collect(fn.Type.Params)
	collect(fn.Type.Results)
	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			collect(node.Type.Params)
			collect(node.Type.Results)
		case *ast.ValueSpec:
			if ident, ok := node.Type.(*ast.Ident); ok && ident.Name == "error" {
				for _, name := range node.Names {
					out[name.Name] = true
				}
			}
		case *ast.AssignStmt:
			if len(node.Lhs) < 2 || len(node.Rhs) != 1 {
				return true
			}
			if _, isCall := node.Rhs[0].(*ast.CallExpr); !isCall {
				return true
			}
			if ident, ok := node.Lhs[len(node.Lhs)-1].(*ast.Ident); ok {
				out[ident.Name] = true
			}
		}
		return true
	})
	return out
}

// statementLists returns every run of statements in a body, which is the
// scope the omission rule is stated in: the check has to follow the call
// and the success path has to follow the check, in the same run.
func statementLists(body *ast.BlockStmt) [][]ast.Stmt {
	var out [][]ast.Stmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BlockStmt:
			out = append(out, node.List)
		case *ast.CaseClause:
			out = append(out, node.Body)
		case *ast.CommClause:
			out = append(out, node.Body)
		}
		return true
	})
	return out
}

// checkSense is which of an if statement's two branches the provider's
// failure takes.
type checkSense int

// The senses a condition can be read with.
const (
	// senseNone is a condition that never compares the provider's error
	// against nil, so the two outcomes are not told apart here at all.
	senseNone checkSense = iota
	// senseAmbiguous is a condition that compares it more than one way
	// round, so which branch is the failure cannot be read off it.
	senseAmbiguous
	// senseFailureInBody is the `err != nil` reading: the body runs on
	// the failure.
	senseFailureInBody
	// senseSuccessInBody is the `err == nil` reading: the body runs on
	// the success and the failure is left to the else or to what the
	// check falls through to.
	senseSuccessInBody
)

// errorCheckSense reads which branch of a check the provider's failure
// takes. The comparison against nil may be one operand of a wider
// condition — a retry that also insists on a non-nil response is still
// an error check — so the condition is searched rather than matched, and
// a negation on the way down flips the reading. A condition that reaches
// the comparison two ways at once is reported as ambiguous rather than
// guessed at: naming the wrong side sends the fix to the branch that was
// already right.
func errorCheckSense(cond ast.Expr, name string) checkSense {
	found := senseNone
	var walk func(expr ast.Expr, negated bool)
	walk = func(expr ast.Expr, negated bool) {
		switch node := expr.(type) {
		case *ast.ParenExpr:
			walk(node.X, negated)
		case *ast.UnaryExpr:
			if node.Op == token.NOT {
				walk(node.X, !negated)
			}
		case *ast.BinaryExpr:
			switch node.Op {
			case token.LAND, token.LOR:
				walk(node.X, negated)
				walk(node.Y, negated)
			case token.NEQ, token.EQL:
				if !comparesNil(node, name) {
					return
				}
				sense := senseSuccessInBody
				if (node.Op == token.NEQ) != negated {
					sense = senseFailureInBody
				}
				switch {
				case found == senseNone:
					found = sense
				case found != sense:
					found = senseAmbiguous
				}
			}
		}
	}
	walk(cond, false)
	return found
}

// comparesNil reports whether a comparison has the named identifier on
// one side and nil on the other, in either order.
func comparesNil(binary *ast.BinaryExpr, name string) bool {
	if isIdent(binary.X, name) && isNilLiteral(binary.Y) {
		return true
	}
	return isNilLiteral(binary.X) && isIdent(binary.Y, name)
}

// isIdent reports whether an expression is the named identifier.
func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// findErrorCheck scans forward from a provider call for the statement
// that checks the error it bound, and returns where in the block it sits
// so the branch that follows the check can be told from the run that
// precedes it.
//
// The check does not have to be the next statement. Binding the elapsed
// time or naming part of the response in between changes nothing about
// the outcome, and a rule that insists on adjacency is one that gets
// code written around it rather than one that catches more: the code
// bends to the scanner, the scanner learns nothing, and the next value
// bound there is reported as a defect it is not.
//
// The search stops at the first statement that transfers control or can
// skip the check, and at any statement that reassigns the error itself.
// Past either of those, a check further down is not this call's — over
// a reassignment it is deciding on another call's outcome, which is the
// defect this rule exists for rather than an exemption from it.
func findErrorCheck(list []ast.Stmt, from int, errIdent string) (*ast.IfStmt, int) {
	for i := from; i < len(list); i++ {
		switch stmt := list[i].(type) {
		case *ast.IfStmt:
			// An init that rebinds the error makes the condition below
			// a different call's check, whatever it is spelled like.
			if init, isAssign := stmt.Init.(*ast.AssignStmt); isAssign && assignsTo(init, errIdent) {
				return nil, -1
			}
			if errorCheckSense(stmt.Cond, errIdent) == senseNone {
				return nil, -1
			}
			return stmt, i
		case *ast.AssignStmt:
			if assignsTo(stmt, errIdent) {
				return nil, -1
			}
		case *ast.ExprStmt, *ast.DeclStmt:
			// Naming a value and calling something for its effect both
			// leave the outcome where it was. A hook call among these is
			// not the check and does not stand in for it: it ran however
			// the call went, so it records nothing about which way.
		default:
			return nil, -1
		}
	}
	return nil, -1
}

// assignsTo reports whether an assignment writes the named identifier.
func assignsTo(assign *ast.AssignStmt, name string) bool {
	for _, lhs := range assign.Lhs {
		if isIdent(lhs, name) {
			return true
		}
	}
	return false
}

// firstChain returns whichever of two routes was found, so a finding
// whose side is not named still says how the branch that does record
// reaches the hook.
func firstChain(a, b []string) []string {
	if a != nil {
		return a
	}
	return b
}

// nilCheckIdent returns the identifier of an `<ident> != nil` condition.
func nilCheckIdent(cond ast.Expr) (string, bool) {
	binary, ok := cond.(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return "", false
	}
	ident, ok := binary.X.(*ast.Ident)
	if !ok || !isNilLiteral(binary.Y) {
		return "", false
	}
	return ident.Name, true
}

// isNilLiteral reports whether an expression is the nil identifier.
func isNilLiteral(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// exprName renders a dotted selector chain, so a finding names the value
// the hook was reached through rather than only the hook.
func exprName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		if prefix := exprName(node.X); prefix != "" {
			return prefix + "." + node.Sel.Name
		}
		return node.Sel.Name
	}
	return ""
}

// dedupe collapses findings that the walk reached twice — a call inside
// nested error checks is inspected once per enclosing check — and orders
// them by position, so a run's output is stable.
func dedupe(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Kind < findings[j].Kind
	})
	var out []Finding
	for _, f := range findings {
		if n := len(out); n > 0 && out[n-1].File == f.File && out[n-1].Line == f.Line && out[n-1].Kind == f.Kind {
			continue
		}
		out = append(out, f)
	}
	return out
}
