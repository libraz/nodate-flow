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

// Resolution is one declaration together with the column it was placed on,
// or the record that no column was found for it.
type Resolution struct {
	Declaration
	Column Column
	// Placed reports whether a column was found. An unplaced declaration
	// is still compared against other surfaces, which needs no column.
	Placed bool
}

// Overflows reports whether the declared bound is larger than the column
// can hold, which is the state where the API accepts a value storage then
// refuses.
func (r Resolution) Overflows() bool { return r.Placed && r.Max > r.Column.Capacity }

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
// under an object is not resolved: it describes a member of something else,
// and the resource the input is named after says nothing about which table
// that member lives in. A query parameter is not resolved either: it
// selects rows rather than supplying a value to store in one.
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

// ResolveAll places every declaration it can, returning the whole set:
// the ones a column was found for carry it, and the rest record that none
// was.
func ResolveAll(decls []Declaration, schema Schema, writeSets map[string]map[string]bool) []Resolution {
	out := make([]Resolution, 0, len(decls))
	for _, d := range decls {
		column, ok := Resolve(d, schema, ScopeFor(d, writeSets))
		out = append(out, Resolution{Declaration: d, Column: column, Placed: ok})
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
// both sides were placed, they pair only if they were placed on the same
// column — two surfaces can name a comment and mean an event's and a task's.
// Where either side reaches no column, the names carry the pairing on their
// own: two REST operations pair only inside one handler package, because a
// noun as ordinary as member names a different table in each package that
// uses it, while a tool pairs with any REST operation on its resource
// because it belongs to no package and its name spells the resource in
// full.
//
// It is separate from Disagreements because the failure mode of a derived
// check is that the derivation stops matching — a renamed suffix, a tool
// naming convention that shifts — and then it passes because it compared
// nothing. The caller asserts on this set for that reason.
func Pairs(all []Resolution) []Pair {
	ordered := append([]Resolution(nil), all...)
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
	if a.Resource == "" || a.Resource != b.Resource {
		return false
	}
	if a.Section != b.Section || a.Name != b.Name {
		return false
	}
	if a.Placed && b.Placed && a.Column.Qualified() != b.Column.Qualified() {
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
