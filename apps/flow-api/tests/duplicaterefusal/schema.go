package duplicaterefusal

// The schema half: which keys on a table a caller can collide.
//
// The tables are read from sql/core/tables and sql/flow/tables, plus the
// constraints directories beside them, rather than from the generated
// sql/schema.sql. The dump is built from these files, so it can lag them —
// and a check that reads the lagging copy answers about a schema nobody is
// running. The two constraint directories are read for the same reason a
// derivation reads both halves of anything: a UNIQUE added by ALTER TABLE
// is a key, and a reader that only knows CREATE TABLE would report the
// table as defenceless.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// publicIDColumn is the row's own externally visible identifier: a UUID v7
// the server generates, never anything a caller supplies.
const publicIDColumn = "public_id"

// Key is one unique constraint on a table: a UNIQUE key, a PRIMARY KEY, or
// a UNIQUE marker written inline on a column.
type Key struct {
	// Name is the key's declared name, or PRIMARY, or the column name for
	// an inline marker.
	Name string
	// Columns are its columns in declaration order, lowercased.
	Columns []string
}

// Render spells the key the way a failure quotes it.
func (k Key) Render() string {
	return k.Name + " (" + strings.Join(k.Columns, ", ") + ")"
}

// holdsPublicID reports whether the key covers the row's own identifier.
//
// Such a key is violated only when a server-generated UUID repeats:
// public_id is unique on its own, so a wider key containing it cannot be
// the first key an insert of a fresh UUID collides on, whatever else the
// key holds. That is why the exclusion needs no companion list of scope
// columns — nothing has to decide whether workspace_id "counts".
func (k Key) holdsPublicID() bool {
	for _, c := range k.Columns {
		if c == publicIDColumn {
			return true
		}
	}
	return false
}

// Table is one table's identity and its keys.
type Table struct {
	// Name is the table name.
	Name string
	// File is the repository-relative file that declares it and Line the
	// line of its CREATE TABLE.
	File string
	Line int
	// Keys are every unique constraint on it, in declaration order.
	Keys []Key
	// generated is the set of columns the database fills in on its own:
	// AUTO_INCREMENT surrogates. A key over nothing else is issued by the
	// sequence rather than supplied by a caller.
	generated map[string]bool
}

// Location renders the table's position for a failure message.
func (t Table) Location() string {
	return fmt.Sprintf("%s:%d", t.File, t.Line)
}

// Collidable returns the keys a caller's own input can violate: the ones
// that cover something other than the row's identifier and the sequence.
//
// These are the keys that make a named duplicate refusal true. A table with
// none of them can still raise ER_DUP_ENTRY, but only by repeating a value
// the server issued — never by repeating what the caller sent.
func (t Table) Collidable() []Key {
	var out []Key
	for _, k := range t.Keys {
		if k.holdsPublicID() || t.allGenerated(k) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// Identifying returns the keys that exist only to hold the row's identity,
// which is what a failure quotes to show what the table does carry.
func (t Table) Identifying() []Key {
	var out []Key
	for _, k := range t.Keys {
		if k.holdsPublicID() || t.allGenerated(k) {
			out = append(out, k)
		}
	}
	return out
}

// allGenerated reports whether every column of the key is filled in by the
// database rather than by a caller.
func (t Table) allGenerated(k Key) bool {
	if len(k.Columns) == 0 {
		return false
	}
	for _, c := range k.Columns {
		if !t.generated[c] {
			return false
		}
	}
	return true
}

// layerDirs are the directories the schema is built from, in the order
// sql/build-schema.sh emits them.
var layerDirs = [][]string{
	{"sql", "core", "tables"},
	{"sql", "core", "constraints"},
	{"sql", "flow", "tables"},
	{"sql", "flow", "constraints"},
}

// ReadTables returns every table the schema declares, keyed by name.
//
// A directory that does not exist is skipped: the two layers do not carry
// the same set of subdirectories, and the build script treats a missing one
// the same way.
func ReadTables(root string) (map[string]Table, error) {
	out := map[string]Table{}
	var alters []alterUnique

	for _, parts := range layerDirs {
		dir := filepath.Join(append([]string{root}, parts...)...)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if err := readLayer(root, dir, out, &alters); err != nil {
			return nil, err
		}
	}

	for _, a := range alters {
		table, ok := out[a.table]
		if !ok {
			continue
		}
		table.Keys = append(table.Keys, a.key)
		out[a.table] = table
	}
	return out, nil
}

// alterUnique is a key added to an already-declared table.
type alterUnique struct {
	table string
	key   Key
}

func readLayer(root, dir string, out map[string]Table, alters *[]alterUnique) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, err := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		text := string(raw)
		for _, table := range parseCreateTables(rel, text) {
			out[table.Name] = table
		}
		*alters = append(*alters, parseAlterUniques(text)...)
		return nil
	})
}

// parseCreateTables reads every CREATE TABLE in one file.
//
// The scan steps over string literals and nested parentheses because the
// column comments in this schema hold both commas and parentheses, which a
// line-oriented split reads as structure.
func parseCreateTables(file, text string) []Table {
	const marker = "CREATE TABLE "
	var tables []Table
	upper := strings.ToUpper(text)

	for offset := 0; ; {
		idx := strings.Index(upper[offset:], marker)
		if idx < 0 {
			return tables
		}
		start := offset + idx + len(marker)
		open := strings.Index(text[start:], "(")
		if open < 0 {
			return tables
		}
		name := strings.Trim(strings.TrimSpace(text[start:start+open]), "`")
		bodyStart := start + open + 1
		bodyEnd := matchingParen(text, bodyStart)
		if bodyEnd < 0 {
			return tables
		}
		table := Table{
			Name:      strings.ToLower(name),
			File:      file,
			Line:      1 + strings.Count(text[:offset+idx], "\n"),
			generated: map[string]bool{},
		}
		for _, item := range splitTopLevel(text[bodyStart:bodyEnd]) {
			table.absorb(item)
		}
		tables = append(tables, table)
		offset = bodyEnd
	}
}

// absorb reads one item of a CREATE TABLE body: a key definition, or a
// column whose own definition may carry a key marker.
func (t *Table) absorb(item string) {
	item = strings.TrimSpace(item)
	rest := item
	// A named constraint states the same key one word later.
	if after, found := cutKeyword(rest, "CONSTRAINT"); found {
		rest = dropIdentifier(after)
	}
	if after, found := cutKeyword(rest, "PRIMARY KEY"); found {
		if cols, ok := firstColumnList(after); ok {
			t.Keys = append(t.Keys, Key{Name: "PRIMARY", Columns: cols})
		}
		return
	}
	if after, found := cutKeyword(rest, "UNIQUE"); found {
		for _, kw := range []string{"KEY", "INDEX"} {
			if trimmed, ok := cutKeyword(after, kw); ok {
				after = trimmed
				break
			}
		}
		name := "UNIQUE"
		if !strings.HasPrefix(strings.TrimSpace(after), "(") {
			name = strings.Trim(strings.Fields(strings.TrimSpace(after))[0], "`")
			if at := strings.Index(name, "("); at > 0 {
				name = name[:at]
			}
			after = dropIdentifier(after)
		}
		if cols, ok := firstColumnList(after); ok {
			t.Keys = append(t.Keys, Key{Name: name, Columns: cols})
		}
		return
	}
	// Anything the key vocabulary does not open is a column, except the
	// index kinds that constrain nothing.
	upper := strings.ToUpper(item)
	for _, prefix := range []string{"KEY ", "INDEX ", "FULLTEXT", "SPATIAL", "FOREIGN KEY", "CHECK "} {
		if strings.HasPrefix(upper, prefix) {
			return
		}
	}
	fields := strings.Fields(item)
	if len(fields) < 2 {
		return
	}
	column := strings.ToLower(strings.Trim(fields[0], "`"))
	definition := beforeComment(item)
	if containsKeyword(definition, "AUTO_INCREMENT") {
		t.generated[column] = true
	}
	if containsKeyword(definition, "PRIMARY KEY") {
		t.Keys = append(t.Keys, Key{Name: "PRIMARY", Columns: []string{column}})
	}
	if containsKeyword(definition, "UNIQUE") {
		t.Keys = append(t.Keys, Key{Name: column, Columns: []string{column}})
	}
}

// parseAlterUniques reads the keys added to an existing table.
func parseAlterUniques(text string) []alterUnique {
	const marker = "ALTER TABLE "
	var out []alterUnique
	upper := strings.ToUpper(text)
	for offset := 0; ; {
		idx := strings.Index(upper[offset:], marker)
		if idx < 0 {
			return out
		}
		start := offset + idx + len(marker)
		end := strings.Index(text[start:], ";")
		if end < 0 {
			return out
		}
		body := text[start : start+end]
		offset = start + end

		fields := strings.Fields(body)
		if len(fields) == 0 {
			continue
		}
		table := strings.ToLower(strings.Trim(fields[0], "`"))
		rest := body[strings.Index(body, fields[0])+len(fields[0]):]
		after, found := cutKeyword(rest, "ADD")
		if !found {
			continue
		}
		if trimmed, ok := cutKeyword(after, "CONSTRAINT"); ok {
			after = dropIdentifier(trimmed)
		}
		after, found = cutKeyword(after, "UNIQUE")
		if !found {
			continue
		}
		for _, kw := range []string{"KEY", "INDEX"} {
			if trimmed, ok := cutKeyword(after, kw); ok {
				after = trimmed
				break
			}
		}
		name := "UNIQUE"
		if !strings.HasPrefix(strings.TrimSpace(after), "(") {
			name = strings.Trim(strings.Fields(strings.TrimSpace(after))[0], "`")
			after = dropIdentifier(after)
		}
		if cols, ok := firstColumnList(after); ok {
			out = append(out, alterUnique{table: table, key: Key{Name: name, Columns: cols}})
		}
	}
}

// cutKeyword removes a leading SQL keyword, reporting whether it was there.
// The keyword has to end on a word boundary so PRIMARY does not match
// PRIMARYISH and UNIQUE does not match a column called unique_hint.
func cutKeyword(text, keyword string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t\n\r")
	if len(trimmed) < len(keyword) || !strings.EqualFold(trimmed[:len(keyword)], keyword) {
		return text, false
	}
	rest := trimmed[len(keyword):]
	if rest != "" && isIdentifierByte(rest[0]) {
		return text, false
	}
	return rest, true
}

// containsKeyword reports whether a definition holds the keyword as a whole
// word, so a column comment mentioning it in prose is not read as one.
func containsKeyword(text, keyword string) bool {
	upper := strings.ToUpper(text)
	for offset := 0; ; {
		at := strings.Index(upper[offset:], keyword)
		if at < 0 {
			return false
		}
		at += offset
		before := at == 0 || !isIdentifierByte(lowerByte(upper[at-1]))
		endAt := at + len(keyword)
		after := endAt >= len(upper) || !isIdentifierByte(lowerByte(upper[endAt]))
		if before && after {
			return true
		}
		offset = at + 1
	}
}

// beforeComment cuts a column definition at its COMMENT clause. The comment
// is prose and can hold any keyword; reading it would let a sentence
// describing a unique key declare one.
func beforeComment(item string) string {
	upper := strings.ToUpper(item)
	for offset := 0; ; {
		at := strings.Index(upper[offset:], "COMMENT")
		if at < 0 {
			return item
		}
		at += offset
		before := at == 0 || !isIdentifierByte(lowerByte(upper[at-1]))
		if before {
			return item[:at]
		}
		offset = at + 1
	}
}

// dropIdentifier removes one leading identifier, which is how a key's name
// sits between its keyword and its column list.
func dropIdentifier(text string) string {
	trimmed := strings.TrimLeft(text, " \t\n\r")
	if strings.HasPrefix(trimmed, "`") {
		if end := strings.Index(trimmed[1:], "`"); end >= 0 {
			return trimmed[end+2:]
		}
		return trimmed
	}
	for i := 0; i < len(trimmed); i++ {
		if !isIdentifierByte(lowerByte(trimmed[i])) {
			return trimmed[i:]
		}
	}
	return ""
}

// firstColumnList reads the parenthesised column list a key is declared
// over. A prefix length — name(16) — is not part of the column name.
func firstColumnList(text string) ([]string, bool) {
	open := strings.Index(text, "(")
	if open < 0 {
		return nil, false
	}
	end := matchingParen(text, open+1)
	if end < 0 {
		return nil, false
	}
	var out []string
	for _, part := range splitTopLevel(text[open+1 : end]) {
		name := strings.TrimSpace(part)
		if at := strings.Index(name, "("); at > 0 {
			name = name[:at]
		}
		name = strings.ToLower(strings.Trim(strings.TrimSpace(name), "`"))
		if name != "" {
			out = append(out, name)
		}
	}
	return out, len(out) > 0
}

func isIdentifierByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func lowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// matchingParen returns the index of the ")" closing the "(" that ends just
// before start, or -1.
func matchingParen(text string, start int) int {
	depth := 1
	for i := start; i < len(text); {
		switch {
		case text[i] == '\'':
			i = skipString(text, i)
		case strings.HasPrefix(text[i:], "/*"):
			i = skipUntil(text, i+2, "*/")
		case strings.HasPrefix(text[i:], "--"):
			i = skipUntil(text, i, "\n")
		case text[i] == '(':
			depth++
			i++
		case text[i] == ')':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return -1
}

func skipString(text string, i int) int {
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '\'' {
			if i+1 < len(text) && text[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
	}
	return i
}

func skipUntil(text string, i int, terminator string) int {
	end := strings.Index(text[i:], terminator)
	if end < 0 {
		return len(text)
	}
	return i + end + len(terminator)
}

// splitTopLevel splits a body on the commas separating its definitions,
// dropping comments and ignoring commas nested inside parentheses.
//
// A string literal is stepped over whole and kept whole: a column comment
// can hold a comma or a parenthesis, and reading inside one would split the
// definition it belongs to.
func splitTopLevel(body string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(body); {
		switch {
		case body[i] == '\'':
			end := skipString(body, i)
			current.WriteString(body[i:end])
			i = end
		case strings.HasPrefix(body[i:], "/*"):
			i = skipUntil(body, i+2, "*/")
		case strings.HasPrefix(body[i:], "--"):
			i = skipUntil(body, i, "\n")
		default:
			switch body[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if body[i] == ',' && depth == 0 {
				items = appendItem(items, current.String())
				current.Reset()
				i++
				continue
			}
			current.WriteByte(body[i])
			i++
		}
	}
	return appendItem(items, current.String())
}

func appendItem(items []string, item string) []string {
	if trimmed := strings.TrimSpace(item); trimmed != "" {
		return append(items, trimmed)
	}
	return items
}
