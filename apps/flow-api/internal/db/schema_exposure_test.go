package db

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exposure markers a primary key declares in its column comment.
//
// The two-tier id scheme has one documented exception: master /
// enumeration tables (roles, themes, locales) carry no public_id, so
// their internal id is not a secret and a foreign key naming one is not
// a leak. That exception cannot be inferred from the shape of a table —
// "has no public_id" is also true of internal bookkeeping tables whose
// keys must stay hidden — so the schema has to say which it is, and the
// checks below read the answer instead of carrying a list of table
// names that would drift.
//
// Absence of a marker is not treated as "master". A table that forgot
// to say fails TestPrimaryKeysDeclareTheirExposure, because a silently
// permissive default is the failure this whole family of checks exists
// to prevent.
const (
	internalPKMarker = "never exposed"
	masterPKMarker   = "safe to expose"
)

// TestPrimaryKeysDeclareTheirExposure requires every integer primary key
// in the schema to state whether it may be published.
//
// This is what makes the exemption in
// [TestGeneratedIDFieldsAreNotSerialized] derivable rather than
// hand-maintained: a new master table declares itself once, in the place
// the column is defined, and both checks pick it up.
func TestPrimaryKeysDeclareTheirExposure(t *testing.T) {
	t.Parallel()

	var undeclared []string
	for _, rel := range parseSchemaRelations(t) {
		comment, ok := rel.Columns["id"]
		if !ok {
			continue
		}
		if strings.Contains(comment, internalPKMarker) || strings.Contains(comment, masterPKMarker) {
			continue
		}
		undeclared = append(undeclared, rel.Name)
	}

	if len(undeclared) > 0 {
		t.Fatalf("a primary key must say in its COMMENT whether it may be published: %q for a "+
			"surrogate key behind a public_id, %q for a master/enumeration table that has none. "+
			"Tables that say neither:\n  %s",
			internalPKMarker, masterPKMarker, strings.Join(undeclared, "\n  "))
	}
}

// TestIndexShapingColumnsAreNotSerialized requires every generated
// column that a UNIQUE key is built on to be excluded from JSON.
//
// A column that is both GENERATED ALWAYS and named in a UNIQUE key
// exists for the index and nothing else — the de-NULLed projections that
// scope a key to live rows (`active`), to a single role
// (`task_singleton_role`), or over a nullable foreign key
// (`event_id_key`, `signal_kind_match`). None of them means anything to
// a client, and each one publishes a fragment of how the table enforces
// uniqueness.
//
// The set is read from the schema rather than listed here, because the
// way this rule was broken before was a rename: the override still named
// the old column, matched nothing, and stopped applying without changing
// a byte of generated code. Deriving the set means a rename moves the
// requirement with it.
func TestIndexShapingColumnsAreNotSerialized(t *testing.T) {
	t.Parallel()

	shaping := indexShapingColumns(t)
	if len(shaping) == 0 {
		t.Fatal("no generated column backs a UNIQUE key; the schema parse is wrong, not the schema")
	}

	var offenders []string
	for _, f := range walkGeneratedFields(t) {
		if strings.HasSuffix(f.Struct, "Params") || !ast.IsExported(f.Name) {
			continue
		}
		column, ok := shaping[normalizeIdentifier(f.Name)]
		if !ok || hasJSONExcludeTag(f.Tag) {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%s:%d %s.%s (%s)", f.Path, f.Line, f.Struct, f.Name, column))
	}

	if len(offenders) > 0 {
		t.Fatalf("a generated column that only shapes a UNIQUE key must not reach a JSON response; "+
			"add the column to the json:\"-\" override list in sql/sqlc.yaml and re-run make gen-sqlc:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// masterForeignKeyColumns returns the column names that may publish an
// integer id: the foreign keys pointing at a master table.
//
// A master table's own `id` is deliberately not exempt. sqlc applies an
// override by column name across every table at once, so exempting `id`
// would exempt every table's primary key; and the convention answers a
// master row with its string key ("member", "aurora-light") rather than
// its sequence position anyway, so nothing needs the number.
func masterForeignKeyColumns(t *testing.T) map[string]string {
	t.Helper()

	relations := parseSchemaRelations(t)
	master := map[string]bool{}
	for _, rel := range relations {
		if strings.Contains(rel.Columns["id"], masterPKMarker) {
			master[rel.Name] = true
		}
	}

	exempt := map[string]string{}
	for _, rel := range relations {
		for column, target := range rel.ForeignKeys {
			if master[target] {
				exempt[normalizeIdentifier(column)] = target
			}
		}
	}
	return exempt
}

// indexShapingColumns returns the generated columns a UNIQUE key is
// built on, keyed by the normalized name a generated Go field carries.
func indexShapingColumns(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, rel := range parseSchemaRelations(t) {
		for column := range rel.Generated {
			if rel.UniqueKeyColumns[column] {
				out[normalizeIdentifier(column)] = rel.Name + "." + column
			}
		}
	}
	return out
}

// ----- schema -----

// schemaRelation is what one CREATE TABLE declares that these checks
// read: its columns and their comments, which of those columns are
// generated, which take part in a UNIQUE key, and where its foreign keys
// point.
type schemaRelation struct {
	Name             string
	Columns          map[string]string // column -> COMMENT text
	Generated        map[string]bool
	UniqueKeyColumns map[string]bool
	ForeignKeys      map[string]string // column -> referenced table
}

// Line prefixes that introduce a table constraint rather than a column.
var schemaConstraintKeywords = map[string]bool{
	"PRIMARY": true, "UNIQUE": true, "KEY": true, "INDEX": true,
	"CONSTRAINT": true, "FOREIGN": true, "FULLTEXT": true,
	"SPATIAL": true, "CHECK": true,
}

func parseSchemaRelations(t *testing.T) []schemaRelation {
	t.Helper()

	text := stripSQLComments(readRepoFile(t, filepath.Join("sql", "schema.sql")))
	var out []schemaRelation
	const marker = "CREATE TABLE "
	for i := 0; ; {
		at := strings.Index(text[i:], marker)
		if at < 0 {
			break
		}
		at += i
		i = at + len(marker)

		fields := strings.Fields(text[i:])
		if len(fields) == 0 {
			break
		}
		open := strings.IndexByte(text[at:], '(')
		if open < 0 {
			break
		}
		open += at
		closing := matchSQLParen(text, open)
		if closing < 0 {
			break
		}
		out = append(out, parseRelationBody(unquoteSQL(strings.TrimSuffix(fields[0], "(")), text[open+1:closing]))
		i = closing
	}
	if len(out) == 0 {
		t.Fatal("no CREATE TABLE found in sql/schema.sql")
	}
	return out
}

func parseRelationBody(name, body string) schemaRelation {
	rel := schemaRelation{
		Name:             name,
		Columns:          map[string]string{},
		Generated:        map[string]bool{},
		UniqueKeyColumns: map[string]bool{},
		ForeignKeys:      map[string]string{},
	}
	for _, item := range splitSQLTopLevel(body) {
		fields := strings.Fields(item)
		if len(fields) < 2 {
			continue
		}
		head := strings.ToUpper(fields[0])
		if !schemaConstraintKeywords[head] {
			column := unquoteSQL(fields[0])
			rel.Columns[column] = columnComment(item)
			if containsWord(item, "GENERATED") && containsWord(item, "ALWAYS") {
				rel.Generated[column] = true
			}
			continue
		}
		if containsWord(item, "UNIQUE") {
			for _, c := range parenthesizedColumns(item) {
				rel.UniqueKeyColumns[c] = true
			}
		}
		if containsWord(item, "FOREIGN") {
			if column, target := foreignKeyTarget(item); column != "" {
				rel.ForeignKeys[column] = target
			}
		}
	}
	return rel
}

// columnComment returns the text of a column's COMMENT '...' clause.
func columnComment(item string) string {
	at := strings.Index(strings.ToUpper(item), "COMMENT ")
	if at < 0 {
		return ""
	}
	rest := item[at+len("COMMENT "):]
	open := strings.IndexByte(rest, '\'')
	if open < 0 {
		return ""
	}
	for i := open + 1; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			i++
		case '\'':
			// A doubled quote is an escaped quote, not the end.
			if i+1 < len(rest) && rest[i+1] == '\'' {
				i++
				continue
			}
			return rest[open+1 : i]
		}
	}
	return rest[open+1:]
}

// parenthesizedColumns returns the identifiers of the first
// parenthesized list in item, which for a key definition is its column
// list.
func parenthesizedColumns(item string) []string {
	open := strings.IndexByte(item, '(')
	if open < 0 {
		return nil
	}
	closing := matchSQLParen(item, open)
	if closing < 0 {
		return nil
	}
	var out []string
	for _, part := range splitSQLTopLevel(item[open+1 : closing]) {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		// A prefix length (`title(64)`) rides on the column name.
		name := unquoteSQL(fields[0])
		if cut := strings.IndexByte(name, '('); cut >= 0 {
			name = name[:cut]
		}
		out = append(out, name)
	}
	return out
}

// foreignKeyTarget returns the single referencing column and the table it
// points at. Composite keys are skipped: none of them names a master
// table and reading one as a single column would be wrong.
func foreignKeyTarget(item string) (column, target string) {
	columns := parenthesizedColumns(item)
	if len(columns) != 1 {
		return "", ""
	}
	at := strings.Index(strings.ToUpper(item), "REFERENCES ")
	if at < 0 {
		return "", ""
	}
	fields := strings.Fields(item[at+len("REFERENCES "):])
	if len(fields) == 0 {
		return "", ""
	}
	name := fields[0]
	if cut := strings.IndexByte(name, '('); cut >= 0 {
		name = name[:cut]
	}
	return columns[0], unquoteSQL(name)
}

// ----- SQL text helpers -----

// stripSQLComments removes -- line comments and /* */ block comments,
// leaving quoted text alone and keeping the line structure intact.
//
// The table documentation in the schema is written in block comments,
// and prose carries apostrophes and unbalanced parentheses. Left in
// place they derail the parenthesis matching that finds a table body,
// and the table disappears from the parse without a word.
func stripSQLComments(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	quote := byte(0)
	inLine, inBlock := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			switch {
			case c == '*' && i+1 < len(text) && text[i+1] == '/':
				inBlock = false
				i++
			case c == '\n':
				b.WriteByte(c)
			}
		case quote != 0:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(text) {
				i++
				b.WriteByte(text[i])
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			inLine = true
			i++
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			inBlock = true
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// matchSQLParen returns the index of the ')' closing the '(' at open.
func matchSQLParen(text string, open int) int {
	depth := 0
	quote := byte(0)
	for i := open; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitSQLTopLevel splits a comma-separated list on the commas that are
// not inside parentheses or quotes.
func splitSQLTopLevel(text string) []string {
	var out []string
	depth := 0
	quote := byte(0)
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(text[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// containsWord reports whether word appears in text as a whole word,
// ignoring case.
func containsWord(text, word string) bool {
	upper := strings.ToUpper(text)
	word = strings.ToUpper(word)
	for i := 0; ; {
		at := strings.Index(upper[i:], word)
		if at < 0 {
			return false
		}
		at += i
		i = at + len(word)
		if at > 0 && isIdentifierByte(upper[at-1]) {
			continue
		}
		if i < len(upper) && isIdentifierByte(upper[i]) {
			continue
		}
		return true
	}
}

func unquoteSQL(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []string{"`", `"`, "'"} {
		s = strings.Trim(s, q)
	}
	return s
}

func isIdentifierByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// normalizeIdentifier reduces a name to the form that a snake_case
// column and the Go field sqlc emits for it share.
//
// This is a comparison key, not a conversion: it never produces a name
// anything is stored or generated under. sqlc remains the only place a
// column name becomes a field name.
func normalizeIdentifier(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// ----- generated sources -----

// generatedField is one struct field in a generated package, with the
// position a failure needs to name it.
type generatedField struct {
	Path   string
	Line   int
	Struct string
	Name   string
	Type   ast.Expr
	Tag    *ast.BasicLit
}

// walkGeneratedFields returns every struct field in every sqlc output
// tree.
//
// This reads the generated source rather than reflecting over the
// packages so it can cover all three trees, including auth-api's, which
// lives in a different module and cannot be imported from here.
func walkGeneratedFields(t *testing.T) []generatedField {
	t.Helper()

	var out []generatedField
	for _, root := range generatedRoots(t) {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := spec.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, f := range st.Fields.List {
					for _, name := range f.Names {
						out = append(out, generatedField{
							Path:   relativeTo(t, path),
							Line:   fset.Position(name.Pos()).Line,
							Struct: spec.Name.Name,
							Name:   name.Name,
							Type:   f.Type,
							Tag:    f.Tag,
						})
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}
