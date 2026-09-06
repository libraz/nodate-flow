package precondition

// The event-kind half of this package: which declared kinds anything in
// the repository actually appends.
//
// A kind is declared in packages/go-shared/eventbus/kinds.go, resolved to
// a family by registry.go, and given a delivery policy — a notification
// title, a resource type, a severity, or silence — by the flow-api
// notification fan-out. Each of those three is a statement about a kind
// that arrives. None of them is a producer, so all three are satisfied by
// a kind nothing ever appends: the constant resolves, the family matches
// its prefix, the fan-out holds a title no user can receive because no
// row carrying that type is ever written.
//
// So the requirement is read off the rest of the tree instead:
//
//	declared   a constant of type Kind in kinds.go, together with the wire
//	           string it is assigned. Both spellings matter, because a
//	           producer may name the constant or write the string.
//	reference  the constant reached through an eventbus package qualifier
//	           in Go, the wire string written as a literal in Go or
//	           TypeScript, or the wire string written into the VALUES of a
//	           named INSERT. A Go reference is read from the syntax tree,
//	           so a kind named in a comment is not one.
//	surface    a file that declares or classifies kinds rather than
//	           emitting them. References there do not count: they name
//	           every kind by construction, so counting them would make
//	           the rule pass on all of them.
//	producing  a language a row of the events table can be written from.
//	           Go and SQL are; TypeScript is not — see
//	           [emissionScope.Emitters].
//	test       a reference from a test file or a test harness. It does not
//	           count either, for the same reason.
//	emitted    at least one reference in a producing language, outside the
//	           surfaces and outside the tests.
//
// A declared kind that is not emitted is answerable, unless it is listed
// as reserved with the reason nothing appends it. [emissionScope.Emitters]
// states what this does not look at.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The two import paths a Go file reaches an event kind through: the
// shared package that declares the constants, and the flow-api package
// that re-exports them under its own name.
const (
	sharedEventbusPath = "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	flowEventbusPath   = modulePath + "/internal/eventbus"
)

// kindDeclarationFile is where the constants are declared, relative to
// the repository root.
const kindDeclarationFile = "packages/go-shared/eventbus/kinds.go"

// EmissionSurface is a file whose references to an event kind are
// declaration or consumption rather than emission.
type EmissionSurface struct {
	// Path is the file, relative to the repository root.
	Path string
	// Reason is why its references cannot be a producer.
	Reason string
}

// emissionSurfaces are the files that name kinds without appending them.
//
// Every one of them names kinds in bulk — that is what they are for — so
// a rule that counted them would report nothing whatever the tree did.
// The list is closed and each entry is checked to still exist, because a
// surface that quietly stops being excluded is the failure mode that has
// no symptom: it turns the rule green on every kind at once.
var emissionSurfaces = []EmissionSurface{
	{
		Path:   kindDeclarationFile,
		Reason: "declares every constant and its wire string",
	},
	{
		Path:   "packages/go-shared/eventbus/registry.go",
		Reason: "enumerates the constants so a consumer can iterate them, and resolves each to a family",
	},
	{
		Path:   "apps/flow-api/internal/eventbus/kinds.go",
		Reason: "re-exports every shared constant under the flow-api package name",
	},
	{
		Path:   "apps/flow-api/internal/notification/fanout.go",
		Reason: "gives every kind a delivery policy: a notification title and severity, or silence",
	},
}

// skippedDirs are directories the scan does not descend into. Each holds
// either third-party code or a copy of something, and a kind found in a
// copy says nothing about what the tree emits today. The generated
// queriers are the copy that matters most: they embed the text of
// sql/queries, which is read directly, so counting them would point a
// failure at a file no fix can be written in.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
	".turbo":       true,
	"backup":       true,
	"generated":    true,
}

// testPathSegments name a directory whose files are test material
// whatever their extension, which covers the harness packages that are
// ordinary .go and the TypeScript suites that are ordinary .ts.
var testPathSegments = map[string]bool{
	"tests":     true,
	"test":      true,
	"e2e":       true,
	"__tests__": true,
	"testdata":  true,
	"fixtures":  true,
}

// EventKind is one declared event kind: the constant a producer may name
// and the wire string a producer may write.
type EventKind struct {
	// Name is the Go constant.
	Name string
	// Wire is the string the constant is assigned, which is what lands in
	// the events table's type column.
	Wire string
	// File and Line are the declaration.
	File string
	Line int
}

// Location renders the declaration for a failure message.
func (k EventKind) Location() string {
	return k.File + ":" + strconv.Itoa(k.Line)
}

// Language is the language a reference was written in, which is what
// decides whether it can be a producer.
type Language string

// The three languages the scan reads.
const (
	LangGo         Language = "Go"
	LangSQL        Language = "SQL"
	LangTypeScript Language = "TypeScript"
)

// EmissionSite is one reference to a kind.
type EmissionSite struct {
	// File and Line are where the reference sits.
	File string
	Line int
	// Form is how the kind was spelled there: the constant name for a
	// qualified Go reference, or the quoted wire string otherwise. A
	// failure that names the form tells the reader which of the two
	// spellings the check found, which is what says whether the other one
	// was searched for in vain.
	Form string
	// Lang is the language of the file.
	Lang Language
	// Test marks a reference from a test file or a test harness.
	Test bool
}

// Produces reports whether the reference can be the thing that appends
// the row.
func (s EmissionSite) Produces() bool {
	return !s.Test && s.Lang != LangTypeScript
}

// Location renders the reference for a failure message.
func (s EmissionSite) Location() string {
	return s.File + ":" + strconv.Itoa(s.Line)
}

// KindMinter is an exported function of the declaring package that
// builds a kind at run time by appending a free-form name to a fixed
// prefix.
//
// It exists because one does: the transition kinds are appended as
// `TaskTransition(name)` with the name carried in the request, so no call
// site names the constant and no reading of the syntax can say which of
// them a given call produces. The declaring package has already settled
// what that means — a runtime-minted kind is covered by its family — and
// this follows the same answer: a call to the minter counts as a producer
// for every declared kind whose wire string starts with the prefix.
type KindMinter struct {
	// Name is the function.
	Name string
	// Prefix is the literal it builds onto, taken from its return
	// expression, so a rename of the namespace moves the coverage with it.
	Prefix string
}

// emissionScope is the repository read for event-kind references.
type emissionScope struct {
	root string
	// kinds are the declared kinds, sorted by constant name.
	kinds []EventKind
	// minters are the run-time kind builders the declaring package
	// exports.
	minters []KindMinter
	// sites holds every reference, keyed by constant name, including the
	// ones a surface or a test contributed. Keeping them rather than
	// filtering during the walk is what lets a failure say "referenced
	// only from a test" instead of "not referenced", which are different
	// defects with different fixes.
	sites map[string][]EmissionSite
	// minted holds the calls to a minter, keyed by the prefix it builds,
	// which stand in as producers for every kind under that prefix.
	minted map[string][]EmissionSite
	// files counts the files the scan read, so an empty result can be
	// told apart from an empty tree.
	files int
	// statements counts the named SQL statements read, for the same
	// reason.
	statements int
	// surfaceHits counts the references dropped for sitting on a surface,
	// per surface. A surface that has stopped matching any file drops
	// nothing, which is the state in which the exclusion has quietly
	// become an inclusion.
	surfaceHits map[string]int
}

// parseEmissionScope reads the declared kinds and then scans the
// repository for references to them.
func parseEmissionScope(root string) (*emissionScope, error) {
	scope := &emissionScope{
		root:        root,
		sites:       map[string][]EmissionSite{},
		minted:      map[string][]EmissionSite{},
		surfaceHits: map[string]int{},
	}
	if err := scope.readKinds(); err != nil {
		return nil, err
	}
	if err := scope.scan(); err != nil {
		return nil, err
	}
	return scope, nil
}

// readKinds reads every `Name Kind = "wire"` constant out of the
// declaration file.
//
// The constants are read rather than the enumeration in registry.go: the
// enumeration is a hand-maintained list, and a kind missing from it is a
// kind this check would never ask about.
func (s *emissionScope) readKinds() error {
	path := filepath.Join(s.root, filepath.FromSlash(kindDeclarationFile))
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Kind" {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			wire, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return uerr
			}
			s.kinds = append(s.kinds, EventKind{
				Name: vs.Names[0].Name,
				Wire: wire,
				File: kindDeclarationFile,
				Line: fset.Position(vs.Pos()).Line,
			})
		}
	}
	sort.Slice(s.kinds, func(i, j int) bool { return s.kinds[i].Name < s.kinds[j].Name })
	s.readMinters(parsed)
	return nil
}

// readMinters reads the run-time kind builders out of the declaration
// file: an exported function returning a Kind whose body returns
// `Kind("prefix" + something)`.
//
// The prefix is taken from the code rather than named here so it cannot
// drift from what the function actually builds, and an empty one is
// dropped: a minter that covered every kind would answer the whole rule
// by itself.
func (s *emissionScope) readMinters(file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !fn.Name.IsExported() {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			continue
		}
		result, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
		if !ok || result.Name != "Kind" {
			continue
		}
		prefix, found := "", false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ret, isReturn := n.(*ast.ReturnStmt)
			if !isReturn || len(ret.Results) != 1 {
				return true
			}
			call, isCall := ret.Results[0].(*ast.CallExpr)
			if !isCall || len(call.Args) != 1 {
				return true
			}
			if ident, isIdent := call.Fun.(*ast.Ident); !isIdent || ident.Name != "Kind" {
				return true
			}
			bin, isBin := call.Args[0].(*ast.BinaryExpr)
			if !isBin || bin.Op != token.ADD {
				return true
			}
			lit, isLit := bin.X.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				return true
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil || value == "" {
				return true
			}
			prefix, found = value, true
			return false
		})
		if found {
			s.minters = append(s.minters, KindMinter{Name: fn.Name.Name, Prefix: prefix})
		}
	}
	sort.Slice(s.minters, func(i, j int) bool { return s.minters[i].Name < s.minters[j].Name })
}

// scan walks the repository for Go and TypeScript references, then reads
// the named SQL statements.
func (s *emissionScope) scan() error {
	if err := s.scanSources(); err != nil {
		return err
	}
	return s.scanStatements()
}

// scanStatements records the kinds written by an INSERT among the sqlc
// statements.
//
// Only an insert counts, and only among the named statements: those are
// the only SQL in the tree that writes a row, and the events table is
// append-only, so a kind that arrives by SQL arrives in a VALUES list. A
// kind matched in a WHERE clause is a query reading events somebody else
// wrote, and a kind quoted in a table's COMMENT is an example in
// documentation — both name the kind without producing it, and counting
// either would let a report or a column description stand in for the
// append.
func (s *emissionScope) scanStatements() error {
	statements, err := Statements(s.root)
	if err != nil {
		return err
	}
	s.statements = len(statements)
	for _, stmt := range statements {
		if head(stmt.SQL) != "insert" {
			continue
		}
		for _, kind := range s.kinds {
			if !strings.Contains(stmt.SQL, "'"+kind.Wire+"'") {
				continue
			}
			s.sites[kind.Name] = append(s.sites[kind.Name], EmissionSite{
				File: stmt.Path,
				Line: stmt.Line,
				Form: "'" + kind.Wire + "' in " + stmt.Name,
				Lang: LangSQL,
			})
		}
	}
	return nil
}

// scanSources walks the repository and records every Go and TypeScript
// reference to a declared kind.
func (s *emissionScope) scanSources() error {
	byWire := map[string]EventKind{}
	byName := map[string]EventKind{}
	for _, kind := range s.kinds {
		byWire[kind.Wire] = kind
		byName[kind.Name] = kind
	}
	surface := map[string]bool{}
	for _, entry := range emissionSurfaces {
		surface[entry.Path] = true
	}

	return filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skippedDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		onSurface := surface[rel]
		isTest := isTestPath(rel)

		switch {
		case strings.HasSuffix(rel, ".go"):
			s.files++
			return s.scanGo(path, rel, onSurface, isTest, byName, byWire)
		case strings.HasSuffix(rel, ".ts"), strings.HasSuffix(rel, ".tsx"):
			s.files++
			return s.scanText(path, rel, LangTypeScript, onSurface, isTest, byWire)
		}
		return nil
	})
}

// scanGo records the two spellings a Go file can name a kind by: the
// constant reached through an eventbus package qualifier, and the wire
// string as a literal.
//
// Reading the syntax tree rather than the text is what keeps a doc
// comment out. Every kind's own comment asserts that it is appended when
// something happens, and a scan that believed those sentences would take
// each kind's declaration as proof of its own emission.
func (s *emissionScope) scanGo(path, rel string, onSurface, isTest bool, byName, byWire map[string]EventKind) error {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}

	// Which local names refer to an eventbus package in this file, plus
	// the unqualified case: inside an eventbus package itself a constant
	// is named bare.
	qualifiers := map[string]bool{}
	for _, imp := range parsed.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath != sharedEventbusPath && importPath != flowEventbusPath {
			continue
		}
		local := importPath[strings.LastIndex(importPath, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		qualifiers[local] = true
	}
	inEventbus := parsed.Name.Name == "eventbus"

	record := func(kind EventKind, form string, pos token.Pos) {
		if onSurface {
			s.surfaceHits[rel]++
			return
		}
		s.sites[kind.Name] = append(s.sites[kind.Name], EmissionSite{
			File: rel,
			Line: fset.Position(pos).Line,
			Form: form,
			Lang: LangGo,
			Test: isTest,
		})
	}

	mint := func(prefix, form string, pos token.Pos) {
		if onSurface {
			s.surfaceHits[rel]++
			return
		}
		s.minted[prefix] = append(s.minted[prefix], EmissionSite{
			File: rel,
			Line: fset.Position(pos).Line,
			Form: form,
			Lang: LangGo,
			Test: isTest,
		})
	}

	ast.Inspect(parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, isSel := node.Fun.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			ident, isIdent := sel.X.(*ast.Ident)
			if !isIdent || !qualifiers[ident.Name] {
				return true
			}
			for _, minter := range s.minters {
				if sel.Sel.Name == minter.Name {
					mint(minter.Prefix, ident.Name+"."+minter.Name+"(...)", node.Pos())
				}
			}
		case *ast.SelectorExpr:
			ident, ok := node.X.(*ast.Ident)
			if !ok || !qualifiers[ident.Name] {
				return true
			}
			if kind, ok := byName[node.Sel.Name]; ok {
				record(kind, ident.Name+"."+kind.Name, node.Pos())
			}
		case *ast.Ident:
			if !inEventbus {
				return true
			}
			if kind, ok := byName[node.Name]; ok {
				record(kind, kind.Name, node.Pos())
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			value, uerr := strconv.Unquote(node.Value)
			if uerr != nil {
				return true
			}
			if kind, ok := byWire[value]; ok {
				record(kind, strconv.Quote(kind.Wire), node.Pos())
			}
		}
		return true
	})
	return nil
}

// scanText records a wire string written as a quoted literal in a SQL or
// TypeScript file.
//
// The quotes are the whole of the discrimination. Requiring them keeps
// the prose that names a kind out, and requiring one on each side keeps
// a longer kind from being read as a shorter one it starts with.
func (s *emissionScope) scanText(path, rel string, lang Language, onSurface, isTest bool, byWire map[string]EventKind) error {
	raw, err := os.ReadFile(path) //#nosec G304 -- repository path walked at test time
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(raw), "\n") {
		for wire, kind := range byWire {
			if !quotedIn(line, wire) {
				continue
			}
			if onSurface {
				s.surfaceHits[rel]++
				continue
			}
			s.sites[kind.Name] = append(s.sites[kind.Name], EmissionSite{
				File: rel,
				Line: i + 1,
				Form: strconv.Quote(wire),
				Lang: lang,
				Test: isTest,
			})
		}
	}
	return nil
}

// quotedIn reports whether the line holds the value with a quote
// character on each side.
func quotedIn(line, value string) bool {
	from := 0
	for {
		at := strings.Index(line[from:], value)
		if at < 0 {
			return false
		}
		at += from
		if at > 0 && isQuoteByte(line[at-1]) {
			after := at + len(value)
			if after < len(line) && isQuoteByte(line[after]) {
				return true
			}
		}
		from = at + 1
	}
}

// isQuoteByte reports whether the byte is one of the three quote
// characters Go, SQL and TypeScript spell a string with.
func isQuoteByte(b byte) bool {
	return b == '\'' || b == '"' || b == '`'
}

// isTestPath reports whether the file is test material: a Go test, a
// TypeScript suite, or anything under a directory that exists to hold
// them.
func isTestPath(rel string) bool {
	base := rel[strings.LastIndex(rel, "/")+1:]
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	for _, segment := range strings.Split(rel, "/") {
		if testPathSegments[segment] {
			return true
		}
	}
	return false
}

// Emitters returns, for one kind, the references that can be a producer:
// a Go or SQL reference outside the declaring and classifying surfaces,
// written somewhere other than test material.
//
// Both exclusions are the same judgement about who writes a row of the
// events table.
//
// TypeScript never does. The API owns the table; the SDK and the web
// apps read what it returns, so a wire string in a .ts or .tsx file is a
// timeline filter, a card renderer or a fixture — a consumer, in exactly
// the way the notification fan-out is a consumer. Counting it would let
// a UI that offers to filter on a kind stand in for the code that
// produces one, and the web app's event filter lists nearly every kind
// there is, so the rule would pass on most of them however the backend
// changed. SQL counts because it genuinely produces: some events are
// appended by a named statement that writes the type as a literal, with
// no Go constant anywhere on the path.
//
// A test reference does not count either. A kind only a test appends has
// no production producer, and the notification copy written for it still
// reaches nobody; counting the test would let a fixture stand in for the
// feature. References of both excluded shapes are kept rather than
// discarded, so a failure can say the kind is mentioned but never
// produced instead of the weaker "not found".
//
// What this does not look at, so a green run is read for what it is:
//
//   - Whether a Go reference is an append. It establishes that production
//     Go names the kind, which is weaker: a kind named only to be matched
//     against, or one passed to something that decides not to write it,
//     reads as emission. The gap this closes is a kind no producer
//     mentions at all, which is what an unbuilt or forgotten append looks
//     like. The SQL half is narrower, because there the statement's verb
//     is legible: only an INSERT counts.
//   - Which kind a minter call produces. A call to a run-time builder
//     covers every declared kind under the prefix it builds, so one
//     transition being appended answers for all of them; whether the
//     names the callers pass ever include a given one is a run-time fact.
//   - Whether the append is reachable. A call inside a function nothing
//     calls, or behind configuration nothing enables, counts.
//   - Whether the doc comment beside the declaration is true. Each kind
//     asserts the condition under which it is appended; this reads only
//     whether an appender exists, not whether it fires on that condition,
//     and least of all whether some other kind is appended in its place.
//   - A wire string assembled from parts anywhere other than a minter.
//     Only a whole literal is matched, so a kind concatenated or
//     interpolated at the call site is invisible, and a minter whose
//     return is written some other way is not derived as one.
//   - SQL outside the sqlc statements: the table definitions, the views,
//     the triggers and the conformance harness are not read for kinds at
//     all.
//   - Any language other than Go, SQL and TypeScript, and any file under
//     a directory in [skippedDirs].
func (s *emissionScope) Emitters(name string) []EmissionSite {
	var out []EmissionSite
	for _, site := range s.sites[name] {
		if site.Produces() {
			out = append(out, site)
		}
	}
	if kind, ok := s.Kind(name); ok {
		for prefix, sites := range s.minted {
			if !strings.HasPrefix(kind.Wire, prefix) {
				continue
			}
			for _, site := range sites {
				if site.Produces() {
					out = append(out, site)
				}
			}
		}
	}
	return sortedSites(out)
}

// Kind returns one declared kind by constant name.
func (s *emissionScope) Kind(name string) (EventKind, bool) {
	for _, kind := range s.kinds {
		if kind.Name == name {
			return kind, true
		}
	}
	return EventKind{}, false
}

// NonProducing returns the references that exist but cannot append the
// row: everything in TypeScript, and every reference from test material.
// It is what lets a failure distinguish a kind the tree has never heard
// of from one it names everywhere except where a row is written.
func (s *emissionScope) NonProducing(name string) []EmissionSite {
	var out []EmissionSite
	for _, site := range s.sites[name] {
		if !site.Produces() {
			out = append(out, site)
		}
	}
	return sortedSites(out)
}

// Unemitted returns every declared kind no production reference names, in
// declaration-name order.
func (s *emissionScope) Unemitted() []EventKind {
	var out []EventKind
	for _, kind := range s.kinds {
		if len(s.Emitters(kind.Name)) == 0 {
			out = append(out, kind)
		}
	}
	return out
}

// sortedSites orders references by file and line so a failure reads the
// same on every machine.
func sortedSites(sites []EmissionSite) []EmissionSite {
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		if sites[i].Line != sites[j].Line {
			return sites[i].Line < sites[j].Line
		}
		return sites[i].Form < sites[j].Form
	})
	return sites
}

// describeSites renders up to three references for a failure message.
func describeSites(sites []EmissionSite) string {
	const limit = 3
	parts := make([]string, 0, limit+1)
	for i, site := range sites {
		if i == limit {
			parts = append(parts, "and "+strconv.Itoa(len(sites)-limit)+" more")
			break
		}
		parts = append(parts, site.Location()+" ("+site.Form+")")
	}
	return strings.Join(parts, ", ")
}
