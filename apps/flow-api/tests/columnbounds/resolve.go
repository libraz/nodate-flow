package columnbounds

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WriteSets returns, for each directory under sql/queries, the tables the
// statements in it write.
//
// A table a surface's own statements never write is not where that
// surface's input lands, however close the names look. That is what
// separates the calendar's events from the timeline's: both are spelled
// `Event` on the wire, and only one of them has a statement behind it in
// the package doing the writing.
func WriteSets(root string) (map[string]map[string]bool, error) {
	base := filepath.Join(root, "sql", "queries")
	out := map[string]map[string]bool{}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		dir := filepath.Base(filepath.Dir(path))
		if _, seen := out[dir]; !seen {
			out[dir] = map[string]bool{}
		}
		for _, table := range writtenTables(string(raw)) {
			out[dir][table] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StatementWrites returns, for each named statement under sql/queries, the
// tables that one statement writes.
//
// This is the same reading as WriteSets at a finer grain, and the grain is
// what makes it usable from the other direction. A query file holds the
// reads beside the writes, so asking a file which tables it writes answers
// for the whole file; asking a statement answers for the method a handler
// calls, which is the thing that says where that handler's values land.
//
// Names are taken to be unique across the tree, which they are because sqlc
// generates a method per name. Should two files ever share one, the tables
// of both are returned together — the candidate set widens, which can only
// turn an answer into no answer, never into a wrong one.
func StatementWrites(root string) (map[string]map[string]bool, error) {
	base := filepath.Join(root, "sql", "queries")
	out := map[string]map[string]bool{}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		for name, body := range splitStatements(string(raw)) {
			if _, seen := out[name]; !seen {
				out[name] = map[string]bool{}
			}
			for _, table := range writtenTables(body) {
				out[name][table] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// statementMarker is the annotation sqlc names a statement with, and which
// becomes the generated method's name.
var statementMarker = regexp.MustCompile(`^\s*--\s*name:\s*([A-Za-z_][A-Za-z0-9_]*)`)

// splitStatements cuts a query file into its named statements. Text before
// the first name belongs to no statement — it is the file's own header — and
// is dropped.
func splitStatements(text string) map[string]string {
	out := map[string]string{}
	name := ""
	var body strings.Builder
	flush := func() {
		if name != "" {
			out[name] += body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		if found := statementMarker.FindStringSubmatch(line); found != nil {
			flush()
			name = found[1]
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return out
}

var writeTarget = regexp.MustCompile(`(?:insert\s+(?:ignore\s+)?into|replace\s+into|update)\s+` + "`?" + `([a-z_][a-z0-9_]*)`)

// writtenTables reads the tables a query file writes.
func writtenTables(text string) []string {
	var out []string
	for _, found := range writeTarget.FindAllStringSubmatch(normalize(text), -1) {
		out = append(out, found[1])
	}
	return out
}

// normalize strips SQL comments, lowercases and collapses whitespace so a
// statement can be matched without caring how it was wrapped.
func normalize(text string) string {
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if code, _, found := strings.Cut(line, "--"); found {
			line = code
		}
		out.WriteString(line)
		out.WriteString(" ")
	}
	return strings.Join(strings.Fields(strings.ToLower(out.String())), " ")
}

// ScopeFor returns the tables a declaration's surface writes.
//
// A REST field is scoped to the query directory that serves its handler
// package, matched on the name with the separators removed and the package
// allowed to be the singular of the directory. An MCP tool is not inside
// any package — the tools reach across the whole product — so its scope is
// every table any statement writes, and a resource that does not name one
// exactly is left unresolved rather than guessed at.
func ScopeFor(d Declaration, writeSets map[string]map[string]bool) map[string]bool {
	if d.Surface == MCP {
		all := map[string]bool{}
		for _, tables := range writeSets {
			for table := range tables {
				all[table] = true
			}
		}
		return all
	}

	pkg := condense(filepath.Base(d.Scope))
	names := map[string]bool{pkg: true}
	for _, plural := range pluralCandidates(pkg) {
		names[plural] = true
	}

	out := map[string]bool{}
	for dir, tables := range writeSets {
		if !names[condense(dir)] {
			continue
		}
		for table := range tables {
			out[table] = true
		}
	}
	return out
}

// condense removes the separators that a directory name and a Go package
// name spell the same word with.
func condense(name string) string {
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
}

// pluralCandidates returns the ways this schema's Rails-style table names
// spell the plural of a noun. Several are produced rather than one because
// which is right depends on the word; the caller settles it by asking which
// of them the schema actually declares, and treats more than one answer as
// no answer.
func pluralCandidates(noun string) []string {
	if noun == "" {
		return nil
	}
	out := []string{noun + "s", noun + "es"}
	if last := noun[len(noun)-1]; last == 'y' && len(noun) > 1 && !isVowel(noun[len(noun)-2]) {
		out = append(out, noun[:len(noun)-1]+"ies")
	}
	return out
}

func isVowel(c byte) bool {
	return strings.IndexByte("aeiou", c) >= 0
}

// Rule names the derivation that placed a declaration.
type Rule string

const (
	// ByName placed it from the resource the owner's name spells.
	ByName Rule = "name"
	// ByCalls placed it from the statements the handler taking it calls.
	ByCalls Rule = "calls"
)

// Evidence is everything a resolution reads outside the declaration itself.
// Each field is derived from committed sources, and a resolution uses two of
// them at once so that neither the naming nor the call graph can place a
// field on its own.
type Evidence struct {
	Schema Schema
	// WriteSets are the tables each query directory's statements write,
	// which is the second half of the name rule.
	WriteSets map[string]map[string]bool
	// Calls are the methods each input type's handler calls, which is the
	// first half of the call rule.
	Calls CallIndex
	// StatementWrites are the tables each named statement writes, which is
	// its second half.
	StatementWrites map[string]map[string]bool
}

// Resolution is one declaration together with the column it was placed on,
// or the record that no column was found for it.
type Resolution struct {
	Declaration
	Column Column
	// Placed reports whether a column was found. An unplaced declaration
	// is still compared against other surfaces, which needs no column.
	Placed bool
	// Rule is which derivation placed it, empty when none did. It is
	// reported rather than kept internal because the two are not equally
	// easy to check by eye: a placement from the name can be confirmed by
	// reading the type's name, and one from the calls cannot.
	Rule Rule
}

// Overflows reports whether the declared bound is larger than the column
// can hold, which is the state where the API accepts a value storage then
// refuses.
func (r Resolution) Overflows() bool { return r.Placed && r.Bounded && r.Max > r.Column.Capacity }

// Absent reports whether the field states no length and lands in a column
// that has one.
//
// Placed already carries the second half: a resolution is placed on a
// column of a length-bounded type, because those are the only ones a lookup
// can answer with. So a placed field that states nothing is one the API
// accepts any length of and storage accepts up to a width — the same gap
// Overflows names, with the wire side left open rather than set too wide.
func (r Resolution) Absent() bool { return r.Placed && !r.Bounded }

// Resolve places a declaration on the column it writes.
//
// The evidence is entirely in committed sources, and both halves have to
// agree:
//
//	the name    the input type or the tool names the resource it writes, and
//	            a table of this schema is that resource pluralised — either
//	            outright, or as the tail of a qualified name such as
//	            calendar_memos for a memo.
//	the queries that table is one the surface's own statements write. A
//	            table nothing in scope writes is not where the field lands,
//	            whatever it is called.
//
// The column is the wire name in the schema's spelling. A field nested
// under an object is not placed by this rule: it describes a member of
// something else, and the resource the input is named after says nothing
// about which table that member lives in — the call rule answers those,
// from the statements rather than from the name. A query parameter is not
// resolved at all: it selects rows rather than supplying a value to store
// in one.
//
// More than one candidate table carrying the column is no answer at all,
// and is reported as unresolved rather than picked between.
func Resolve(d Declaration, schema Schema, scope map[string]bool) (Column, bool) {
	if d.Resource == "" || d.Section != "body" || strings.Contains(d.Name, ".") {
		return Column{}, false
	}
	column := snake(d.Name)

	plurals := map[string]bool{}
	for _, p := range pluralCandidates(d.Resource) {
		plurals[p] = true
	}

	var exact, tail []string
	for table := range scope {
		switch {
		case plurals[table]:
			exact = append(exact, table)
		default:
			for p := range plurals {
				if strings.HasSuffix(table, "_"+p) {
					tail = append(tail, table)
					break
				}
			}
		}
	}
	candidates := exact
	if len(candidates) == 0 {
		candidates = tail
	}

	var found []Column
	for _, table := range candidates {
		if c, ok := schema.Column(table, column); ok {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return Column{}, false
	}
	return found[0], true
}

// ResolveByCalls places a declaration on the column the handler that takes
// it writes, and is the answer for the operations whose name states nothing.
//
// It runs on what the name rule leaves over, and it asks for two agreeing
// pieces of evidence of its own:
//
//	the calls  the function taking this input calls a named statement.
//	the write  that statement writes a table carrying a column named as
//	           the field. A statement that writes nothing, or writes
//	           somewhere without that column, places nothing.
//
// Neither half is a list anybody maintains. An operation reaches storage by
// calling the statement that does the storing, whatever the operation is
// called, and the statement says which table it writes in its own text.
//
// A field nested under an object is resolved here, and only here. The name
// rule cannot place a member of another object, because the resource the
// input is named after says nothing about which table that member lives in;
// this rule never asks the name. It asks which statements the handler calls
// and which tables those write, and that question is as well posed for the
// title of a step as for a field of the body itself, so the column is the
// last segment of the field's path in the schema's spelling.
//
// The same discipline as the name rule settles ambiguity: a handler that
// writes two tables both carrying the column has told us the field could
// land in either, which is not an answer, so it stays unresolved. That is
// also what keeps a nested placement honest, and it is not a proof: a member
// whose name happens to match a column of a table the handler writes for
// some other reason is placed all the same, which is why every placement
// from this rule is printed to be checked by eye.
func ResolveByCalls(d Declaration, ev Evidence) (Column, bool) {
	if d.Surface != REST || d.Section != "body" {
		return Column{}, false
	}
	methods := ev.Calls.Methods(d.Scope, d.Owner)
	if len(methods) == 0 {
		return Column{}, false
	}

	written := map[string]bool{}
	for method := range methods {
		for table := range ev.StatementWrites[method] {
			written[table] = true
		}
	}

	column := snake(memberName(d.Name))
	var found []Column
	for table := range written {
		if c, ok := ev.Schema.Column(table, column); ok {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return Column{}, false
	}
	return found[0], true
}

// memberName returns the field a wire path names, which is its last segment:
// the objects it is nested under say where the value sits in the request,
// and the column it lands in is named after the member itself.
func memberName(path string) string {
	if at := strings.LastIndex(path, "."); at >= 0 {
		return path[at+1:]
	}
	return path
}

// ResolveAll places every declaration it can, returning the whole set:
// the ones a column was found for carry it, and the rest record that none
// was.
//
// The name rule goes first and the call rule sees only what it did not
// place. They agree where both apply — a create handler calls the create
// statement for the resource it is named after — and keeping the order fixed
// means the weaker evidence never overrides the stronger one on a handler
// that happens to touch two tables.
func ResolveAll(decls []Declaration, ev Evidence) []Resolution {
	out := make([]Resolution, 0, len(decls))
	for _, d := range decls {
		if column, ok := Resolve(d, ev.Schema, ScopeFor(d, ev.WriteSets)); ok {
			out = append(out, Resolution{Declaration: d, Column: column, Placed: true, Rule: ByName})
			continue
		}
		if column, ok := ResolveByCalls(d, ev); ok {
			out = append(out, Resolution{Declaration: d, Column: column, Placed: true, Rule: ByCalls})
			continue
		}
		out = append(out, Resolution{Declaration: d})
	}
	return out
}

// Placed splits a resolution set into the ones on a column and the ones
// that reached none.
func Placed(all []Resolution) (placed, unplaced []Resolution) {
	for _, r := range all {
		if r.Placed {
			placed = append(placed, r)
			continue
		}
		unplaced = append(unplaced, r)
	}
	return placed, unplaced
}

// Overflows narrows a resolution set down to the bounds no column behind
// them can hold.
func Overflows(all []Resolution) []Resolution {
	var out []Resolution
	for _, r := range all {
		if r.Overflows() {
			out = append(out, r)
		}
	}
	return out
}

// Absent narrows a resolution set down to the fields that state no length
// and land in a column that has one.
func Absent(all []Resolution) []Resolution {
	var out []Resolution
	for _, r := range all {
		if r.Absent() {
			out = append(out, r)
		}
	}
	return out
}

// Stated splits a resolution set into the fields that state a length and the
// ones that state none.
//
// The two derivations that compare a declared number read the first; the one
// that reads the absence of a number reads the second. Keeping them apart is
// what stops a field with nothing to say from being read as a field saying
// zero: a declaration with no bound has a Max of zero, which is smaller than
// every width and different from every other surface's number, so it would
// otherwise overflow nothing while disagreeing with everything.
func Stated(all []Resolution) (stated, unstated []Resolution) {
	for _, r := range all {
		if r.Bounded {
			stated = append(stated, r)
			continue
		}
		unstated = append(unstated, r)
	}
	return stated, unstated
}

// Pair is two declarations of the same field on surfaces that have to agree.
type Pair struct{ A, B Resolution }

// Disagrees reports whether the two state different bounds.
func (p Pair) Disagrees() bool { return p.A.Max != p.B.Max }

// Pairs returns every pair of declarations describing one field of one
// resource on two surfaces.
//
// The comparison itself asks nothing of the schema: two declarations of the
// same field that state different bounds cannot both be the column's width,
// so one of them is wrong whichever way it goes, and that holds for fields
// no resolution reaches.
//
// What the schema is used for is deciding they are the same field. Where
// both sides were placed, the column settles it and nothing else is asked:
// they pair if they were placed on the same one — two surfaces can name a
// comment and mean an event's and a task's — and they pair across packages,
// because two operations demonstrably writing one column are writing one
// column whatever trees they live in. Where either side reaches no column,
// the names carry the pairing on their own: two REST operations pair only
// inside one handler package, because a noun as ordinary as member names a
// different table in each package that uses it, while a tool pairs with any
// REST operation on its resource because it belongs to no package and its
// name spells the resource in full.
//
// It is separate from Disagreements because the failure mode of a derived
// check is that the derivation stops matching — a renamed suffix, a tool
// naming convention that shifts — and then it passes because it compared
// nothing. The caller asserts on this set for that reason.
func Pairs(all []Resolution) []Pair {
	// Only the fields that state a length are compared. Two surfaces
	// stating nothing agree about nothing, and one stating nothing against
	// one stating a number is the absence derivation's question rather than
	// this one's.
	ordered, _ := Stated(all)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Line < ordered[j].Line
	})

	var out []Pair
	for i := range ordered {
		for j := i + 1; j < len(ordered); j++ {
			if describeSameField(ordered[i], ordered[j]) {
				out = append(out, Pair{A: ordered[i], B: ordered[j]})
			}
		}
	}
	return out
}

// describeSameField reports whether two declarations describe one field.
func describeSameField(a, b Resolution) bool {
	if a.Section != b.Section || a.Name != b.Name {
		return false
	}
	if a.Placed && b.Placed {
		return a.Column.Qualified() == b.Column.Qualified() &&
			(a.Surface != b.Surface || a.Owner != b.Owner)
	}
	if a.Resource == "" || a.Resource != b.Resource {
		return false
	}
	if a.Surface != b.Surface {
		return true
	}
	return a.Surface == REST && a.Scope == b.Scope && a.Owner != b.Owner
}

// Disagreements narrows the pairs down to the ones stating different
// bounds.
func Disagreements(all []Resolution) []Pair {
	var out []Pair
	for _, p := range Pairs(all) {
		if p.Disagrees() {
			out = append(out, p)
		}
	}
	return out
}
