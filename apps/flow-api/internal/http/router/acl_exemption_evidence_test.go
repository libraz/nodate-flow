// Package router exemption-evidence checks.
//
// A mutating operation that runs without a group role floor has to be
// authorised somewhere else. Recording where in prose does not survive: the
// sentence stays true-looking after the call it describes is deleted, and a
// whole surface can end up outside the router's reach with nothing but a
// note saying it is fine.
//
// So the exemption table records names instead of sentences, and the checks
// here resolve every name against the source the server is built from:
//
//   - a handler-call exemption names functions that must be reachable from
//     the function the router hands huma.Register;
//   - a group-middleware exemption names middleware every chi group
//     registering the operation must mount;
//   - an actor-scoped-write exemption names the statements the operation
//     runs, each with the column that binds its rows to the caller, and the
//     statement text has to constrain that column.
//
// Every one of those fails when the thing it names stops existing or stops
// being reached, which is the property the prose never had.
//
// The call graph is deliberately literal: a call is an edge only when the
// callee is an identifier in the same package or a function in a package the
// file imports. A method call on a value (deps.Queries.X) contributes its
// name — that is how a statement is matched — but no edge, so a helper is
// never credited to a package-level function that merely shares its name.
package router

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
)

// aclExemption records where a floorless mutating operation's authorization
// lives, in terms a check can resolve against the source.
type aclExemption struct {
	// via is the kind of evidence that authorises the operation. It decides
	// which of the fields below has to be filled in.
	via auth.Enforcement
	// checks names Go functions as "package.Function": functions the
	// registered handler must reach for auth.EnforcedByHandlerCall,
	// middleware the operation's route group must mount for
	// auth.EnforcedByGroupMiddleware.
	checks []string
	// writes lists the statements an auth.EnforcedByActorScopedWrite
	// operation runs, each with the column that binds its rows to the
	// caller.
	writes []actorScopedWrite
	// note is context for whoever reads the table. It is never the evidence:
	// everything above resolves against the source, so an operation whose
	// check was deleted fails here while its note still describes one.
	note string
}

// actorScopedWrite names one sqlc query together with the column that ties
// every row it touches to a single user.
type actorScopedWrite struct {
	query  string
	column string
}

// handlerRef identifies the function the router hands huma.Register as an
// operation's handler.
type handlerRef struct {
	pkg  string
	name string
	pos  string
}

// funcSource is one package-level function together with the import names
// its file uses, so a qualified call inside the body resolves to a package.
type funcSource struct {
	pkg     string
	decl    *ast.FuncDecl
	imports map[string]string
}

// routerGroup is one chi group the router builds: the middleware it mounts
// and the operations registered on it.
type routerGroup struct {
	middleware map[string]bool
	ops        map[string]bool
	pos        string
}

// httpSource is the parsed flow-api HTTP tree.
type httpSource struct {
	fset          *token.FileSet
	funcs         map[string]map[string]*funcSource
	registrations map[string]handlerRef
	groups        []routerGroup
}

// parseHTTPSource parses every non-test file under internal/http and indexes
// the three things the exemption checks ask about: package-level functions,
// operation registrations, and the router's chi groups.
func parseHTTPSource(t *testing.T) *httpSource {
	t.Helper()
	src := &httpSource{
		fset:          token.NewFileSet(),
		funcs:         map[string]map[string]*funcSource{},
		registrations: map[string]handlerRef{},
	}

	type parsed struct {
		path string
		file *ast.File
	}
	var files []parsed
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(src.fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		files = append(files, parsed{path: path, file: file})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/http: %v", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	for _, p := range files {
		pkg := p.file.Name.Name
		imports := fileImports(p.file)
		if src.funcs[pkg] == nil {
			src.funcs[pkg] = map[string]*funcSource{}
		}
		for _, decl := range p.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			src.funcs[pkg][fn.Name.Name] = &funcSource{pkg: pkg, decl: fn, imports: imports}
		}
	}

	for _, p := range files {
		pkg := p.file.Name.Name
		imports := fileImports(p.file)
		ast.Inspect(p.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isHumaRegister(call) || len(call.Args) < 3 {
				return true
			}
			id := operationIDArg(call.Args[1])
			if id == "" {
				return true
			}
			hpkg, hname, ok := rootSymbol(call.Args[2], pkg, imports)
			if !ok {
				return true
			}
			src.registrations[id] = handlerRef{
				pkg:  hpkg,
				name: hname,
				pos:  src.fset.Position(call.Pos()).String(),
			}
			return true
		})
	}

	for _, p := range files {
		if filepath.Base(p.path) != "router.go" {
			continue
		}
		src.groups = append(src.groups, src.collectGroups(p.file)...)
	}
	return src
}

// collectGroups reads the r.Group(func(sub chi.Router){...}) blocks out of
// router.go, pairing the middleware each one mounts with the operations it
// ends up registering — including the ones registered indirectly, through a
// package's Register* helper.
func (s *httpSource) collectGroups(file *ast.File) []routerGroup {
	pkg := file.Name.Name
	imports := fileImports(file)
	locals := localSymbols(file, pkg, imports)

	var groups []routerGroup
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		group := routerGroup{
			middleware: map[string]bool{},
			ops:        map[string]bool{},
			pos:        s.fset.Position(call.Pos()).String(),
		}
		ast.Inspect(lit.Body, func(inner ast.Node) bool {
			c, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if us, ok := c.Fun.(*ast.SelectorExpr); ok && us.Sel.Name == "Use" && len(c.Args) == 1 {
				if sym := mountedSymbol(c.Args[0], pkg, imports, locals); sym != "" {
					group.middleware[sym] = true
				}
			}
			return true
		})
		s.collectOps(lit.Body, pkg, imports, group.ops, map[string]bool{})
		groups = append(groups, group)
		return true
	})
	return groups
}

// collectOps records every OperationID registered inside body, following
// calls to Register* helpers into the packages that define them.
func (s *httpSource) collectOps(body ast.Node, pkg string, imports map[string]string, ops, visited map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isHumaRegister(call) && len(call.Args) >= 2 {
			if id := operationIDArg(call.Args[1]); id != "" {
				ops[id] = true
			}
			return true
		}
		cpkg, cname, ok := rootSymbol(call.Fun, pkg, imports)
		if !ok || !strings.HasPrefix(cname, "Register") {
			return true
		}
		key := cpkg + "." + cname
		if visited[key] {
			return true
		}
		visited[key] = true
		byName := s.funcs[cpkg]
		if byName == nil || byName[cname] == nil {
			return true
		}
		target := byName[cname]
		s.collectOps(target.decl.Body, target.pkg, target.imports, ops, visited)
		return true
	})
}

// groupsRegistering returns the chi groups that register the operation.
func (s *httpSource) groupsRegistering(opID string) []routerGroup {
	var out []routerGroup
	for _, g := range s.groups {
		if g.ops[opID] {
			out = append(out, g)
		}
	}
	return out
}

// reachableFrom walks the call graph out of the named function and returns
// the package-qualified functions it reaches, plus every call name seen along
// the way (which is how a statement performed through a generated query
// method is matched).
func (s *httpSource) reachableFrom(ref handlerRef) (qualified, names map[string]bool) {
	qualified = map[string]bool{}
	names = map[string]bool{}
	visited := map[string]bool{}
	queue := []string{ref.pkg + "." + ref.name}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		pkg, name, ok := splitSymbol(cur)
		if !ok {
			continue
		}
		byName := s.funcs[pkg]
		if byName == nil || byName[name] == nil {
			continue
		}
		fn := byName[name]
		ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				names[callee.Name] = true
				sym := fn.pkg + "." + callee.Name
				qualified[sym] = true
				queue = append(queue, sym)
			case *ast.SelectorExpr:
				names[callee.Sel.Name] = true
				qualifier, ok := callee.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkgName, ok := fn.imports[qualifier.Name]
				if !ok {
					// A method on a value, not a package function. Its name is
					// recorded above; crediting it to a same-named function
					// here is how a check would appear to run without doing so.
					return true
				}
				sym := pkgName + "." + callee.Sel.Name
				qualified[sym] = true
				queue = append(queue, sym)
			}
			return true
		})
	}
	return qualified, names
}

// declares reports whether the tree defines the named package-level function.
func (s *httpSource) declares(symbol string) bool {
	pkg, name, ok := splitSymbol(symbol)
	if !ok {
		return false
	}
	byName := s.funcs[pkg]
	return byName != nil && byName[name] != nil
}

// TestRoleFloorExemptionsNameCodeThatRuns resolves every exemption against
// the source: the named check has to exist, and it has to be on the path the
// request takes.
//
// This is what stops the exemption table degenerating into documentation.
// Deleting a resolveCalendarWrite call from a calendar handler, or naming a
// function that was renamed away, fails here rather than passing quietly on
// the strength of a sentence.
func TestRoleFloorExemptionsNameCodeThatRuns(t *testing.T) {
	t.Parallel()

	src := parseHTTPSource(t)
	if len(src.registrations) < 100 {
		t.Fatalf("the source walk found only %d operation registrations; the checks below would be looking at nothing", len(src.registrations))
	}
	if len(src.groups) == 0 {
		t.Fatal("the source walk found no chi groups; group-middleware exemptions could not be checked")
	}
	statements := parseQueryStatements(t)
	if len(statements) == 0 {
		t.Fatal("no sqlc statements were read; actor-scoped exemptions could not be checked")
	}

	ids := make([]string, 0, len(roleFloorExemptOps))
	for id := range roleFloorExemptOps {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		ex := roleFloorExemptOps[id]
		ref, registered := src.registrations[id]
		if !registered {
			t.Errorf("roleFloorExemptOps lists %q, but no huma.Register in internal/http names that operation — the exemption cannot be resolved against anything", id)
			continue
		}
		if strings.TrimSpace(ex.note) == "" {
			t.Errorf("%s carries no note; the named check is the evidence, but a reader still needs to be told what it protects", id)
		}

		switch ex.via {
		case auth.EnforcedByHandlerCall:
			checkHandlerCallExemption(t, src, id, ex, ref)
		case auth.EnforcedByGroupMiddleware:
			checkGroupMiddlewareExemption(t, src, id, ex)
		case auth.EnforcedByActorScopedWrite:
			checkActorScopedExemption(t, src, statements, id, ex, ref)
		default:
			t.Errorf("%s declares enforcement %q, which is not one of the kinds in the shared vocabulary", id, ex.via)
		}
	}
}

// checkHandlerCallExemption proves the handler reaches every check the
// exemption names.
func checkHandlerCallExemption(t *testing.T, src *httpSource, id string, ex aclExemption, ref handlerRef) {
	t.Helper()
	if len(ex.checks) == 0 {
		t.Errorf("%s claims its handler authorises it but names no check", id)
		return
	}
	if len(ex.writes) > 0 {
		t.Errorf("%s declares handler-call enforcement and also lists actor-scoped writes; one operation has one kind of evidence", id)
	}
	if !src.declares(ref.pkg + "." + ref.name) {
		t.Errorf("%s is registered with handler %s.%s (%s), which is not a package-level function in internal/http — the call graph has no entry point",
			id, ref.pkg, ref.name, ref.pos)
		return
	}
	reached, _ := src.reachableFrom(ref)
	for _, check := range ex.checks {
		if !src.declares(check) {
			t.Errorf("%s names %s as its check, but no such function exists in internal/http", id, check)
			continue
		}
		if !reached[check] {
			t.Errorf("%s runs without a role floor because %s authorises it, but nothing reachable from its handler %s.%s (%s) calls that function — restore the call, or correct the exemption to name the check that does run",
				id, check, ref.pkg, ref.name, ref.pos)
		}
	}
}

// checkGroupMiddlewareExemption proves every chi group that registers the
// operation mounts the middleware the exemption names.
func checkGroupMiddlewareExemption(t *testing.T, src *httpSource, id string, ex aclExemption) {
	t.Helper()
	if len(ex.checks) == 0 {
		t.Errorf("%s claims its route group authorises it but names no middleware", id)
		return
	}
	groups := src.groupsRegistering(id)
	if len(groups) == 0 {
		t.Errorf("%s claims its route group authorises it, but no chi group in router.go was found registering it", id)
		return
	}
	for _, check := range ex.checks {
		if !src.declares(check) {
			t.Errorf("%s names %s as its middleware, but no such function exists in internal/http", id, check)
			continue
		}
		for _, g := range groups {
			if !g.middleware[check] {
				t.Errorf("%s runs without a role floor because its group mounts %s, but the group at %s registers it without mounting that middleware",
					id, check, g.pos)
			}
		}
	}
}

// checkActorScopedExemption proves the operation performs exactly the
// statements it claims, that each one binds its rows to a single user, and
// that the user comes from the session rather than the request.
func checkActorScopedExemption(t *testing.T, src *httpSource, statements map[string]string, id string, ex aclExemption, ref handlerRef) {
	t.Helper()
	if len(ex.writes) == 0 {
		t.Errorf("%s claims every row it writes is bound to the caller but names no statement", id)
		return
	}
	if len(ex.checks) > 0 {
		t.Errorf("%s declares actor-scoped writes and also names handler checks; one operation has one kind of evidence", id)
	}
	reached, called := src.reachableFrom(ref)

	// The caller has to be the authenticated one. Binding a row to a user id
	// taken from the request body is not caller scoping, it is impersonation
	// with extra steps.
	const actorSource = "middleware.ActorFromContext"
	if !reached[actorSource] {
		t.Errorf("%s is exempt because its rows belong to the caller, but nothing reachable from %s.%s (%s) reads the actor through %s",
			id, ref.pkg, ref.name, ref.pos, actorSource)
	}

	for _, w := range ex.writes {
		if !called[w.query] {
			t.Errorf("%s declares it writes through %s, but nothing reachable from its handler %s.%s calls that query — the exemption describes a statement the operation no longer runs",
				id, w.query, ref.pkg, ref.name)
		}
		stmt, ok := statements[w.query]
		if !ok {
			t.Errorf("%s names statement %s, which no file under sql/queries defines", id, w.query)
			continue
		}
		if !bindsToActor(stmt, w.column) {
			t.Errorf("%s is exempt because %s binds every row it touches to the caller through %s, but the statement neither constrains nor fills that column — the rows it changes are not the caller's alone",
				id, w.query, w.column)
		}
	}
}

// parseQueryStatements reads sql/queries and returns each named statement's
// body, comments stripped.
func parseQueryStatements(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "..", "sql", "queries")
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		raw, rerr := os.ReadFile(path) // #nosec G304,G122 -- fixed in-repo query tree walked at test time
		if rerr != nil {
			return rerr
		}
		name := ""
		var body []string
		flush := func() {
			if name != "" {
				out[name] = strings.Join(body, "\n")
			}
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "-- name:") {
				flush()
				name, body = "", nil
				if fields := strings.Fields(strings.TrimPrefix(trimmed, "-- name:")); len(fields) > 0 {
					name = fields[0]
				}
				continue
			}
			if strings.HasPrefix(trimmed, "--") {
				continue
			}
			body = append(body, line)
		}
		flush()
		return nil
	})
	if err != nil {
		t.Fatalf("walk sql/queries: %v", err)
	}
	return out
}

// bindsToActor reports whether the statement ties the rows it touches to one
// user: an UPDATE or DELETE has to constrain the column, an INSERT has to
// fill it in.
func bindsToActor(stmt, column string) bool {
	normalized := strings.Join(strings.Fields(stmt), " ")
	if strings.HasPrefix(strings.ToUpper(normalized), "INSERT") {
		return namesColumn(normalized, column)
	}
	return constrainsColumn(normalized, column)
}

// constrainsColumn looks for "<column> = ?" with an identifier boundary in
// front, so user_id is not satisfied by recipient_user_id.
func constrainsColumn(s, column string) bool {
	needle := column + " = ?"
	idx := 0
	for idx < len(s) {
		found := strings.Index(s[idx:], needle)
		if found < 0 {
			return false
		}
		at := idx + found
		if at == 0 || !isIdentByte(s[at-1]) {
			return true
		}
		idx = at + len(needle)
	}
	return false
}

// namesColumn reports whether the statement mentions the column as a whole
// identifier.
func namesColumn(s, column string) bool {
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return !isIdentRune(r) }) {
		if tok == column {
			return true
		}
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func isIdentRune(r rune) bool {
	if r < 0 || r > 127 {
		return false
	}
	return isIdentByte(byte(r))
}

// isHumaRegister reports whether the call is huma.Register.
func isHumaRegister(call *ast.CallExpr) bool {
	fun := call.Fun
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	if idx, ok := fun.(*ast.IndexListExpr); ok {
		fun = idx.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Register" {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	return ok && qualifier.Name == "huma"
}

// operationIDArg reads the OperationID out of a huma.Operation literal.
func operationIDArg(arg ast.Expr) string {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	if sel, ok := lit.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Operation" {
		return ""
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "OperationID" {
			continue
		}
		if val, ok := kv.Value.(*ast.BasicLit); ok && val.Kind == token.STRING {
			return strings.Trim(val.Value, `"`)
		}
	}
	return ""
}

// rootSymbol resolves an expression to the package-level function at its
// root: Create(deps) in package tasks resolves to tasks.Create, and
// calendars.CreateEvent(deps) to calendars.CreateEvent.
func rootSymbol(expr ast.Expr, pkg string, imports map[string]string) (string, string, bool) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return rootSymbol(e.Fun, pkg, imports)
	case *ast.Ident:
		return pkg, e.Name, true
	case *ast.SelectorExpr:
		qualifier, ok := e.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		pkgName, ok := imports[qualifier.Name]
		if !ok {
			return "", "", false
		}
		return pkgName, e.Sel.Name, true
	default:
		return "", "", false
	}
}

// mountedSymbol resolves the argument of a sub.Use call to the function it
// comes from, following one level of local variable when the middleware was
// built earlier in the builder.
func mountedSymbol(expr ast.Expr, pkg string, imports, locals map[string]string) string {
	if id, ok := expr.(*ast.Ident); ok {
		return locals[id.Name]
	}
	p, name, ok := rootSymbol(expr, pkg, imports)
	if !ok {
		return ""
	}
	return p + "." + name
}

// localSymbols maps each local variable assigned a package function's result
// to that function, so middleware stored in a variable before being mounted
// still resolves.
func localSymbols(file *ast.File, pkg string, imports map[string]string) map[string]string {
	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if _, isCall := assign.Rhs[0].(*ast.CallExpr); !isCall {
			return true
		}
		p, fn, ok := rootSymbol(assign.Rhs[0], pkg, imports)
		if !ok {
			return true
		}
		out[name.Name] = p + "." + fn
		return true
	})
	return out
}

// fileImports maps the name a file refers to each import by onto that
// import's package name.
func fileImports(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		pkg := packageNameFromPath(path)
		local := pkg
		if imp.Name != nil {
			local = imp.Name.Name
		}
		out[local] = pkg
	}
	return out
}

// packageNameFromPath derives a package name from its import path, skipping a
// trailing major-version element.
func packageNameFromPath(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && isMajorVersion(last) {
		last = parts[len(parts)-2]
	}
	return last
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitSymbol splits "package.Function" into its halves.
func splitSymbol(symbol string) (string, string, bool) {
	idx := strings.Index(symbol, ".")
	if idx <= 0 || idx == len(symbol)-1 {
		return "", "", false
	}
	if strings.Contains(symbol[idx+1:], ".") {
		return "", "", false
	}
	return symbol[:idx], symbol[idx+1:], true
}
