package precondition

// The dependency-bundle half of this package: which fields of a struct a
// package receives from its callers are load-bearing, and which literals
// of that struct leave one holding nil.
//
// A bundle is a plain struct, so a literal that omits a field compiles
// and the omission arrives as a nil pointer. Where the collaborator on
// the other end of that pointer tolerates a nil receiver — mutationlog's
// recorder logs a dropped change rather than failing the request, because
// failing a request over a lost log row is worse — the result is a
// configuration that runs, answers correctly and records nothing. Neither
// the type system nor a shared constructor a caller is free not to call
// says the field had to be set.
//
// So the requirement is read off the code that consumes the bundle,
// rather than declared beside it or listed here:
//
//	bundle     a struct declared under internal/ that an exported function
//	           of its own package takes as a parameter, and that carries no
//	           method of its own. Together those say the caller assembles
//	           it and it is a bundle of collaborators rather than an object
//	           — and they name no package, so a handler package written
//	           next month is a bundle the day its first exported handler
//	           takes one.
//	reached    the declaring package calls through the field —
//	           d.F.Method(...) for a collaborator, d.F(...) for a hook. A
//	           field the package only reads and hands on is not reached
//	           through: that covers every string, bool and int of
//	           configuration, and every optional client a router forwards
//	           into a sub-bundle without using itself.
//	silent     the field is a pointer to a type whose own methods answer
//	           for a nil receiver. That is what makes the omission
//	           invisible. A nil handle or a nil querier faults on first
//	           use, so a literal that leaves one out cannot reach the code
//	           that needs it and stay green — those enforce themselves and
//	           are left alone.
//	settable   the field is exported. A caller outside the package cannot
//	           write an unexported field at all, so holding a literal to
//	           one would state a requirement no literal can meet.
//	guarded    the declaring package compares the field against nil
//	           somewhere in its own non-test code. A package that tests a
//	           field has said the nil state is one it handles, whichever
//	           branch the test guards.
//	required   reached, silent, settable, and not guarded.
//	enforcing  reached, settable, not guarded, able to hold nil, and not
//	           silent — a collaborator that faults the first time anything
//	           reaches it. Omitting one is not a defect; it is a statement
//	           that the literal was never meant to reach that far.
//
// A literal is then answerable for the required fields it does not name,
// and for a required field it names as nil — the same misconfiguration
// stated out loud rather than left out. Which literals are held to that
// is the scope question, and [bundleSource.Literals] answers it along
// with what this does not look at.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// bundleFile is one parsed file in the flow-api tree, together with what
// its package qualifiers resolve to.
type bundleFile struct {
	path       string
	file       *ast.File
	importPath string
	// test marks a _test.go file. A requirement is read only out of
	// non-test files: a package's own tests are callers of the bundle, so
	// a nil check written in one says nothing about what the handlers do.
	test    bool
	imports map[string]string
}

// bundleSource is the flow-api tree parsed for this check.
//
// It is a separate read from [Parse] because the two need opposite
// things. Reachability is about what the shipped code does, so it reads
// internal/ without its tests; a literal that omits a required field is
// most often written in a test, so this reads the whole app including
// every _test.go.
type bundleSource struct {
	fset  *token.FileSet
	root  string
	files []*bundleFile
	// structs maps "importPath.TypeName" onto the declared struct.
	structs map[string]*bundleStruct
	// tolerant holds "importPath.TypeName" for every type in the tree
	// with a pointer method that answers for a nil receiver instead of
	// panicking on one.
	tolerant map[string]bool
	// interfaces holds "importPath.TypeName" for every interface the tree
	// declares, which is one of the shapes a field can hold nil in.
	interfaces map[string]bool
}

// bundleStruct is one struct declaration, kept with the file it was
// declared in so its field types resolve against that file's imports.
type bundleStruct struct {
	key    string
	owner  *bundleFile
	fields []bundleFieldDecl
	// input is set when an exported function of the declaring package
	// takes the struct as a parameter.
	input bool
	// method is set when the declaring package declares a method on the
	// struct, which makes it an object with behaviour of its own rather
	// than a bundle somebody hands in.
	method bool
}

// bundleFieldDecl is one named field of a struct declaration.
type bundleFieldDecl struct {
	name string
	typ  ast.Expr
}

// BundleField is one field a bundle's own package calls through,
// together with where it does so.
type BundleField struct {
	// Name is the field name.
	Name string
	// UseFile and UseLine are the first call through the field in source
	// order, so a failure sends the reader to the code that will hold the
	// nil rather than only to the literal.
	UseFile string
	UseLine int
}

// Location renders the call site for a failure message.
func (f BundleField) Location() string {
	return f.UseFile + ":" + strconv.Itoa(f.UseLine)
}

// BundleType is a caller-assembled struct whose own package calls
// through at least one of its nil-able exported fields.
type BundleType struct {
	// Key is "importPath.TypeName".
	Key string
	// Name is "package.TypeName", which is how a literal spells it.
	Name string
	// Required are the fields a literal has to set, sorted by name.
	Required []BundleField
	// Enforcing are the collaborators that fault the first time the
	// package reaches them, sorted by name. They are what a literal's
	// completeness is read from: whoever leaves one out cannot have
	// meant the code past it to run, because it would not have run.
	Enforcing []BundleField
}

// BundleLiteral is every composite literal of a bundle the walk finds,
// together with whatever it leaves nil and whether the rule holds it to
// that.
//
// Clean literals and unanswerable ones are kept rather than dropped. A
// derived check fails by matching nothing, and an empty finding list is
// what that looks like from the outside; holding the whole set is what
// lets a caller tell a literal the rule read and let past from one that
// is no longer being read at all.
type BundleLiteral struct {
	// Type is the bundle the literal builds.
	Type BundleType
	// Omitted are the required fields the literal does not name.
	Omitted []BundleField
	// ExplicitNil are the required fields the literal names as nil, which
	// is the same configuration stated rather than forgotten.
	ExplicitNil []BundleField
	// Answerable is whether the literal has to be complete. False means
	// the rule read it and let it past, which is a different fact from
	// the literal not being here.
	Answerable bool
	// File and Line are where the literal is written.
	File string
	Line int
}

// Incomplete reports whether the literal leaves a required field nil,
// whether or not it is answerable for that.
func (l BundleLiteral) Incomplete() bool {
	return len(l.Omitted) > 0 || len(l.ExplicitNil) > 0
}

// Reportable reports whether the literal is one the rule fails on.
func (l BundleLiteral) Reportable() bool {
	return l.Answerable && l.Incomplete()
}

// Location renders the literal's position for a failure message.
func (l BundleLiteral) Location() string {
	return l.File + ":" + strconv.Itoa(l.Line)
}

// Names renders the fields left nil, each with the call site that makes
// it required, for a failure message.
func (l BundleLiteral) Names() string {
	parts := make([]string, 0, len(l.Omitted)+len(l.ExplicitNil))
	for _, field := range l.Omitted {
		parts = append(parts, field.Name+" (omitted; called through at "+field.Location()+")")
	}
	for _, field := range l.ExplicitNil {
		parts = append(parts, field.Name+" (set to nil; called through at "+field.Location()+")")
	}
	return strings.Join(parts, ", ")
}

// parseBundleSource reads every hand-written Go file under
// apps/flow-api, tests included, and indexes its type declarations.
//
// The generated queriers are skipped: they declare no bundle, and their
// row structs are built by generated code alone.
func parseBundleSource(root string) (*bundleSource, error) {
	src := &bundleSource{
		fset:       token.NewFileSet(),
		root:       root,
		structs:    map[string]*bundleStruct{},
		tolerant:   map[string]bool{},
		interfaces: map[string]bool{},
	}
	base := filepath.Join(root, "apps", "flow-api")

	packageNames := map[string]string{}
	var paths []string
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "generated" || entry.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	for _, path := range paths {
		parsed, perr := parser.ParseFile(src.fset, path, nil, 0)
		if perr != nil {
			return nil, perr
		}
		rel, relErr := filepath.Rel(base, filepath.Dir(path))
		if relErr != nil {
			return nil, relErr
		}
		importPath := modulePath
		if rel != "." {
			importPath += "/" + filepath.ToSlash(rel)
		}
		test := strings.HasSuffix(path, "_test.go")
		if !test {
			packageNames[importPath] = parsed.Name.Name
		}
		src.files = append(src.files, &bundleFile{
			path:       path,
			file:       parsed,
			importPath: importPath,
			test:       test,
		})
	}

	for _, f := range src.files {
		f.imports = fileImports(f.file, packageNames)
	}
	src.indexTypes()
	src.indexTolerant()
	src.markInputs()
	return src, nil
}

// indexTypes records every struct and every interface the tree declares
// outside its tests. A bundle is production wiring, so a type only a
// test declares is not one; the interfaces are indexed alongside because
// a field naming one holds nil until a caller fills it.
func (s *bundleSource) indexTypes() {
	for _, f := range s.files {
		if f.test {
			continue
		}
		for _, decl := range f.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Assign.IsValid() {
					continue
				}
				if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
					s.interfaces[f.importPath+"."+ts.Name.Name] = true
					continue
				}
				typ, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				st := &bundleStruct{key: f.importPath + "." + ts.Name.Name, owner: f}
				for _, field := range typ.Fields.List {
					for _, name := range field.Names {
						st.fields = append(st.fields, bundleFieldDecl{name: name.Name, typ: field.Type})
					}
				}
				if len(st.fields) > 0 {
					s.structs[st.key] = st
				}
			}
		}
	}
}

// indexTolerant records the types whose pointer methods answer for a nil
// receiver instead of panicking on one.
//
// That property is what makes an omitted field invisible rather than
// loud. A nil database handle or a nil querier faults on first use, so a
// literal that leaves one out cannot reach the code that needs it and
// stay green — the omission enforces itself. A collaborator that checks
// its own receiver has, correctly, chosen to keep serving the request:
// mutationlog logs a dropped change rather than failing a write that
// already committed, and the audit recorder does nothing at all. Then
// there is no fault, no failed assertion and no difference in the
// response, and the only remaining evidence is a missing row nobody
// queries.
func (s *bundleSource) indexTolerant() {
	for _, f := range s.files {
		if f.test {
			continue
		}
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			recv := fn.Recv.List[0]
			star, ok := recv.Type.(*ast.StarExpr)
			if !ok || len(recv.Names) != 1 || recv.Names[0].Name == "_" {
				continue
			}
			ident, ok := star.X.(*ast.Ident)
			if !ok {
				continue
			}
			name := recv.Names[0].Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
					return true
				}
				if isIdentNamed(bin.X, name) && isNilIdent(bin.Y) ||
					isIdentNamed(bin.Y, name) && isNilIdent(bin.X) {
					s.tolerant[f.importPath+"."+ident.Name] = true
				}
				return true
			})
		}
	}
}

// isIdentNamed reports whether the expression is exactly the named
// identifier.
func isIdentNamed(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// markInputs records which structs are a caller's to assemble: taken by
// an exported function of the declaring package, and carrying no method
// of their own.
//
// Both halves are the same statement said from two sides. A struct a
// package accepts across its exported surface is configuration somebody
// else writes, and a struct with no methods is a bundle of collaborators
// rather than an object — nobody builds a partial one to call one of its
// own methods, which is what a half-filled object literal is usually
// for. What is left is the shape the wiring is written in.
func (s *bundleSource) markInputs() {
	for _, f := range s.files {
		if f.test {
			continue
		}
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Recv != nil {
				if st := s.structs[f.receiverKey(fn)]; st != nil {
					st.method = true
				}
				continue
			}
			if !fn.Name.IsExported() {
				continue
			}
			for _, key := range f.bundleBindings(fn, s.structs) {
				if st := s.structs[key]; st != nil {
					st.input = true
				}
			}
		}
	}
}

// receiverKey names the struct a method is declared on.
func (f *bundleFile) receiverKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if index, ok := expr.(*ast.IndexExpr); ok {
		expr = index.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return f.importPath + "." + ident.Name
}

// relPosition returns a position as a repository-relative file and a
// line, so what a failure reports is the same string on every machine.
func (s *bundleSource) relPosition(pos token.Pos) (string, int) {
	at := s.fset.Position(pos)
	path := at.Filename
	if s.root != "" {
		if rel, err := filepath.Rel(s.root, path); err == nil {
			path = rel
		}
	}
	return filepath.ToSlash(path), at.Line
}

// callSite is one call made through a bundle field.
type callSite struct {
	file string
	line int
	pos  token.Pos
}

// Bundles derives every caller-assembled struct in the tree together
// with the fields a literal of it has to set.
//
// Only the declaring package is read. A field is required because the
// code that receives the bundle calls through it, and that code is what
// the literal's author is configuring; what some other package does with
// a value of its own is a statement about a different value.
func (s *bundleSource) Bundles() []BundleType {
	called := map[string]map[string]callSite{}
	guarded := map[string]map[string]bool{}

	note := func(key, field string, pos token.Pos) {
		if called[key] == nil {
			called[key] = map[string]callSite{}
		}
		if prev, ok := called[key][field]; ok && prev.pos <= pos {
			return
		}
		file, line := s.relPosition(pos)
		called[key][field] = callSite{file: file, line: line, pos: pos}
	}
	mark := func(key, field string) {
		if guarded[key] == nil {
			guarded[key] = map[string]bool{}
		}
		guarded[key][field] = true
	}

	for _, f := range s.files {
		if f.test {
			continue
		}
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			binds := f.bundleBindings(fn, s.structs)
			if len(binds) == 0 {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					// d.F(...): the field is the thing being run.
					if key, field, ok := bundleField(node.Fun, binds); ok {
						note(key, field, node.Pos())
						return true
					}
					// d.F.Method(...): the call goes through the field, so
					// a nil there is a nil receiver.
					if sel, isSel := node.Fun.(*ast.SelectorExpr); isSel {
						if key, field, ok := bundleField(sel.X, binds); ok {
							note(key, field, node.Pos())
						}
					}
				case *ast.BinaryExpr:
					if node.Op != token.EQL && node.Op != token.NEQ {
						return true
					}
					if key, field, ok := bundleField(node.X, binds); ok && isNilIdent(node.Y) {
						mark(key, field)
					}
					if key, field, ok := bundleField(node.Y, binds); ok && isNilIdent(node.X) {
						mark(key, field)
					}
				}
				return true
			})
		}
	}

	var out []BundleType
	for key, fields := range called {
		st := s.structs[key]
		if st == nil || !st.input || st.method {
			continue
		}
		var required, enforcing []BundleField
		for _, decl := range st.fields {
			at, reached := fields[decl.name]
			if !reached || guarded[key][decl.name] {
				continue
			}
			if !ast.IsExported(decl.name) {
				continue
			}
			field := BundleField{Name: decl.name, UseFile: at.file, UseLine: at.line}
			switch {
			case s.silentlyNil(decl.typ, st.owner):
				required = append(required, field)
			case s.nilable(decl.typ, st.owner):
				enforcing = append(enforcing, field)
			}
		}
		// A bundle with nothing required is kept. It is in scope and it
		// owes nothing, which is a different statement from not having
		// been looked at, and the controls rest on the difference.
		sort.Slice(required, func(i, j int) bool { return required[i].Name < required[j].Name })
		sort.Slice(enforcing, func(i, j int) bool { return enforcing[i].Name < enforcing[j].Name })
		out = append(out, BundleType{
			Key:       key,
			Name:      shortTypeName(key),
			Required:  required,
			Enforcing: enforcing,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// silentlyNil reports whether the field's zero value is a nil the code
// on the other side answers for rather than faulting on — the only shape
// in which an omitted field survives a test run.
//
// It is a pointer to a type this tree declares, and that type has a
// pointer method written to handle a nil receiver. A type from another
// module cannot be read for that property and is left out, which loses
// findings rather than inventing them.
func (s *bundleSource) silentlyNil(expr ast.Expr, owner *bundleFile) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch typ := star.X.(type) {
	case *ast.Ident:
		return s.tolerant[owner.importPath+"."+typ.Name]
	case *ast.SelectorExpr:
		qualifier, isIdent := typ.X.(*ast.Ident)
		if !isIdent {
			return false
		}
		path, imported := owner.imports[qualifier.Name]
		return imported && s.tolerant[path+"."+typ.Sel.Name]
	}
	return false
}

// nilable reports whether an omitted field of this type arrives as nil
// rather than as a usable zero value.
//
// It decides which reached fields count as enforcing, and that set only
// ever takes literals out of scope, so an uncertain type is answered
// generously — a pointer, a func, a map, a slice, a channel, and any
// interface this tree declares. A named type from another module is
// answered no: it cannot be read, and calling it nil-able would let a
// literal that omits an ordinary value field claim it was never meant to
// run.
func (s *bundleSource) nilable(expr ast.Expr, owner *bundleFile) bool {
	switch typ := expr.(type) {
	case *ast.StarExpr, *ast.FuncType, *ast.MapType, *ast.ChanType, *ast.InterfaceType:
		return true
	case *ast.ArrayType:
		return typ.Len == nil
	case *ast.Ident:
		return s.interfaces[owner.importPath+"."+typ.Name]
	case *ast.SelectorExpr:
		qualifier, isIdent := typ.X.(*ast.Ident)
		if !isIdent {
			return false
		}
		path, imported := owner.imports[qualifier.Name]
		return imported && s.interfaces[path+"."+typ.Sel.Name]
	}
	return false
}

// bundleBindings maps each parameter and receiver name of the function
// onto the struct it carries, for structs declared in the function's own
// package.
//
// Pointer and value forms bind the same way: a field selector reads
// through either, and a bundle passed by pointer holds the same nil in a
// field its literal left out. The receiver is bound for the same reason
// it is recorded at all — a struct that has one is an object rather than
// a bundle, and is dropped from the derivation on that ground rather
// than by being read differently.
func (f *bundleFile) bundleBindings(fn *ast.FuncDecl, structs map[string]*bundleStruct) map[string]string {
	out := map[string]string{}
	add := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			expr := field.Type
			if star, ok := expr.(*ast.StarExpr); ok {
				expr = star.X
			}
			ident, ok := expr.(*ast.Ident)
			if !ok {
				continue
			}
			key := f.importPath + "." + ident.Name
			if structs[key] == nil {
				continue
			}
			for _, name := range field.Names {
				if name.Name != "_" {
					out[name.Name] = key
				}
			}
		}
	}
	add(fn.Recv)
	if fn.Type != nil {
		add(fn.Type.Params)
	}
	return out
}

// bundleField reads a bundle field selector, "d.F", out of an
// expression, returning the bundle it belongs to and the field name.
func bundleField(expr ast.Expr, binds map[string]string) (key, field string, ok bool) {
	sel, isSel := expr.(*ast.SelectorExpr)
	if !isSel {
		return "", "", false
	}
	ident, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	key, bound := binds[ident.Name]
	if !bound {
		return "", "", false
	}
	return key, sel.Sel.Name, true
}

// isNilIdent reports whether the expression is the untyped nil, which is
// the value an omitted field of a nil-able type holds.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// shortTypeName renders "importPath.TypeName" the way a literal spells
// it, "package.TypeName".
func shortTypeName(key string) string {
	dot := strings.LastIndex(key, ".")
	if dot < 0 {
		return key
	}
	path := key[:dot]
	return path[strings.LastIndex(path, "/")+1:] + key[dot:]
}

// Literals returns every composite literal of a bundle in the tree,
// each marked with whether it has to be complete.
//
// Completeness is demanded of three shapes:
//
//   - a literal in a file that is not a _test.go — the deployed wiring
//     and the shared test harness alike. Neither gets to pick which path
//     runs, so both answer for every field their package reaches;
//   - a literal inside a function that hands the bundle back, whatever
//     the file. A helper that builds a bundle for its callers cannot know
//     what any of them will drive, so leaving a field for them to fill is
//     leaving it to whoever forgets;
//   - a literal that names every enforcing field of its bundle. Whoever
//     wired all of them has already paid for the whole path: nothing left
//     in the bundle can turn the run back, so the code that reaches the
//     silent collaborator is code this literal is asking to run. This is
//     the one that reaches a test which builds its own bundle inline and
//     drives the real path with it.
//
// The last is what an incomplete literal in a test is measured against,
// and the measurement is the literal's own contents rather than a guess
// at which branch runs. A test that asserts on a rejected signature, a
// missing workspace or a protocol handshake leaves the querier and the
// handle out too — it has to, since wiring them for a path that returns
// first is work with no purpose — and that omission is the author saying
// where the request stops. A bundle with no enforcing field offers no
// such statement, so an inline literal of one is never answerable; that
// loses findings rather than inventing them, and inventing them on
// correct code is how a check gets switched off.
//
// What it does not look at, so a green run is read for what it is:
//
//   - Which branch of the handler the caller actually drives. Nothing
//     here is control flow or reachability from the call the literal
//     feeds: a fully wired literal handed to the one operation in the
//     package that records nothing is answerable all the same, and an
//     early-return test that wires every enforcing field for reasons of
//     its own is reported.
//
//   - What a field is worth once the package hands it on instead of
//     calling through it. Such a field is neither required nor
//     enforcing, so a literal can leave out a database handle the
//     package only passes to a helper and still be read as fully wired.
//
//   - Whether a value given to an enforcing field is usable. Any
//     expression other than the literal nil counts as wired, so a stub,
//     a zero-value struct pointer or a handle to a closed database all
//     say the same thing here.
//
//   - A named type from outside this module in a non-pointer field. It
//     cannot be read for whether it holds nil, so it is never enforcing,
//     and a literal that omits one still reads as fully wired.
//
//   - A bundle that is not written as a composite literal: a zero value
//     declared and then filled in by assignment, or a bundle a caller
//     received, copied and mutated.
//
//   - A literal whose type the source does not spell: an element of a
//     slice, array or map literal written as {...} without repeating the
//     element type, and any literal reached through a type alias.
//
//   - A required field set to something that is nil at run time. Only the
//     literal nil is read, so a nil-valued variable, a constructor that
//     can return nil, and a typed nil placed in an interface field all
//     pass.
//
//   - Calls the declaring package makes through anything other than a
//     parameter of the bundle type: through a local copy, through a field
//     of another struct the bundle was stored in, or inside a function
//     the field's value was passed to. Each of those leaves a genuinely
//     required field reading as optional.
//
//   - Whether a nil check the package writes actually guards the call.
//     One comparison anywhere in the package makes the field optional for
//     the whole package. That is deliberate: a package that tests a field
//     has said nil is a state it handles, and a rule that argued
//     otherwise from control flow would start firing on the optional
//     configuration every router bundle carries.
//
//   - Unexported fields, fields whose type cannot hold nil, and every
//     value-typed piece of configuration — secrets, URLs, flags, budgets.
//     An omitted string is a zero value too, but what a correct one is
//     cannot be read off the code.
//
//   - Any collaborator that faults on a nil receiver. Those enforce
//     themselves: a test that reaches one and finds it missing goes red
//     on the panic, which is the whole reason the silent ones are what
//     this reads.
func (s *bundleSource) Literals(bundles []BundleType) []BundleLiteral {
	byKey := map[string]BundleType{}
	for _, bundle := range bundles {
		byKey[bundle.Key] = bundle
	}

	var out []BundleLiteral
	for _, f := range s.files {
		// Every node is stacked, not only the functions. ast.Inspect
		// signals the end of any node's children with a nil call, so a
		// stack that pushed selectively would unwind on the first nested
		// expression and report the enclosing function as absent.
		var ancestors []ast.Node
		ast.Inspect(f.file, func(n ast.Node) bool {
			if n == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return true
			}
			ancestors = append(ancestors, n)
			lit, isLit := n.(*ast.CompositeLit)
			if !isLit || lit.Type == nil {
				return true
			}
			bundle, isBundle := byKey[f.literalTypeKey(lit.Type)]
			if !isBundle {
				return true
			}
			omitted, explicit, readable := missingRequired(lit, bundle)
			if !readable {
				return true
			}
			file, line := s.relPosition(lit.Pos())
			out = append(out, BundleLiteral{
				Type:        bundle,
				Omitted:     omitted,
				ExplicitNil: explicit,
				Answerable:  !f.test || f.produces(bundle.Key, ancestors) || fullyWired(lit, bundle),
				File:        file,
				Line:        line,
			})
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// missingRequired reads which of the bundle's required fields the
// literal leaves nil.
//
// readable is false for a literal that names its fields by position: it
// sets every one of them, so there is nothing it can have left out.
func missingRequired(lit *ast.CompositeLit, bundle BundleType) (omitted, explicit []BundleField, readable bool) {
	named := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, isKV := elt.(*ast.KeyValueExpr)
		if !isKV {
			return nil, nil, false
		}
		if key, isIdent := kv.Key.(*ast.Ident); isIdent {
			named[key.Name] = kv.Value
		}
	}
	for _, field := range bundle.Required {
		value, set := named[field.Name]
		switch {
		case !set:
			omitted = append(omitted, field)
		case isNilIdent(value):
			explicit = append(explicit, field)
		}
	}
	return omitted, explicit, true
}

// fullyWired reports whether the literal names every enforcing field of
// its bundle with something other than nil.
//
// That is the evidence a literal gives about how far it meant to get.
// The enforcing fields are the collaborators that fault on first use, so
// a literal missing one stops at the first line that reaches it, and a
// literal holding all of them has nothing left in the bundle to stop it.
// A bundle with no enforcing field gives no evidence either way, and no
// evidence is answered as not answerable.
func fullyWired(lit *ast.CompositeLit, bundle BundleType) bool {
	if len(bundle.Enforcing) == 0 {
		return false
	}
	named := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, isKV := elt.(*ast.KeyValueExpr)
		if !isKV {
			return false
		}
		if key, isIdent := kv.Key.(*ast.Ident); isIdent {
			named[key.Name] = kv.Value
		}
	}
	for _, field := range bundle.Enforcing {
		value, set := named[field.Name]
		if !set || isNilIdent(value) {
			return false
		}
	}
	return true
}

// produces reports whether any enclosing function hands the bundle back
// to a caller, which is what makes a literal inside a test file
// answerable for every required field.
func (f *bundleFile) produces(key string, ancestors []ast.Node) bool {
	for _, node := range ancestors {
		var results *ast.FieldList
		switch fn := node.(type) {
		case *ast.FuncDecl:
			if fn.Type != nil {
				results = fn.Type.Results
			}
		case *ast.FuncLit:
			if fn.Type != nil {
				results = fn.Type.Results
			}
		default:
			continue
		}
		if results == nil {
			continue
		}
		for _, result := range results.List {
			expr := result.Type
			if star, isStar := expr.(*ast.StarExpr); isStar {
				expr = star.X
			}
			if f.literalTypeKey(expr) == key {
				return true
			}
		}
	}
	return false
}

// literalTypeKey resolves the type a composite literal names onto
// "importPath.TypeName", for the two spellings a literal has: the bare
// name inside the declaring package and the qualified name outside it.
func (f *bundleFile) literalTypeKey(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return f.importPath + "." + node.Name
	case *ast.SelectorExpr:
		qualifier, ok := node.X.(*ast.Ident)
		if !ok {
			return ""
		}
		path, imported := f.imports[qualifier.Name]
		if !imported {
			return ""
		}
		return path + "." + node.Sel.Name
	}
	return ""
}
