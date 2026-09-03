// Command check-sqlc-overrides verifies that every override selector in
// sql/sqlc.yaml names something that actually exists.
//
// The generated data layer is already compared byte for byte against a
// fresh generation (scripts/check-codegen-drift.sh), which catches almost
// everything. It cannot catch this: an override that matches zero columns
// produces exactly the same output as no override at all, so there is no
// diff to find. A column renamed in the schema, a typo in a selector, or a
// table that lost the column silently turns the rule off, and the JSON tag
// or the Go type the override existed to enforce simply stops applying.
// Nothing in the pipeline says a word.
//
// What a selector may name:
//   - "*.column"       — a column of that name anywhere
//   - "table.column"   — a column of a specific table or view
//   - a db_type        — a column type used somewhere in the schema
//
// A selector matches when the name exists as a column of a table or view
// in sql/schema.sql, or as a result alias in sql/queries/**. Aliases count
// because sqlc applies overrides to query result columns too, and several
// of them (agent_public_id, task_internal_id) exist only under an alias.
//
// Usage: go run scripts/check-sqlc-overrides.go
//
// Exit codes:
//
//	0 — every selector matches at least one column
//	1 — a selector matches nothing
//
//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	configPath := filepath.Join(root, "sql", "sqlc.yaml")
	schemaPath := filepath.Join(root, "sql", "schema.sql")
	queriesDir := filepath.Join(root, "sql", "queries")

	idx, err := indexSchema(schemaPath)
	if err != nil {
		return err
	}
	if len(idx.columns) == 0 {
		return fmt.Errorf("check-sqlc-overrides: no columns found in %s", relPath(root, schemaPath))
	}
	aliases, err := queryAliases(queriesDir)
	if err != nil {
		return err
	}

	selectors, err := readSelectors(configPath)
	if err != nil {
		return err
	}
	if len(selectors) == 0 {
		return fmt.Errorf("check-sqlc-overrides: no overrides found in %s", relPath(root, configPath))
	}

	var dead []string
	for _, s := range selectors {
		if reason := s.unmatched(idx, aliases); reason != "" {
			dead = append(dead, fmt.Sprintf("%s:%d: %s", relPath(root, configPath), s.line, reason))
		}
	}
	if len(dead) > 0 {
		return fmt.Errorf("check-sqlc-overrides: %d override selector(s) match no column:\n\n  %s\n\n"+
			"An override that matches nothing is not an error sqlc reports and not a difference the\n"+
			"generated code shows — it just stops applying. Fix the name or delete the override.",
			len(dead), strings.Join(dead, "\n  "))
	}
	fmt.Printf("check-sqlc-overrides: %d override selectors, all matched\n", len(selectors))
	return nil
}

// ----- sqlc.yaml -----

// selector is one override entry: what it names, and where it is written.
type selector struct {
	kind  string // "column" or "db_type"
	value string
	line  int
}

// unmatched returns an empty string when the selector names something that
// exists, and a human-readable reason when it does not.
func (s selector) unmatched(idx *schemaIndex, aliases map[string]bool) string {
	if s.kind == "db_type" {
		if idx.dbTypes[strings.ToLower(baseType(s.value))] {
			return ""
		}
		return fmt.Sprintf("db_type %q is not a column type used in sql/schema.sql", s.value)
	}
	table, column, qualified := strings.Cut(s.value, ".")
	if !qualified {
		column = s.value
	}
	switch {
	case qualified && table != "*":
		cols, known := idx.relations[strings.ToLower(table)]
		if !known {
			return fmt.Sprintf("column %q names %q, which is not a table or view in sql/schema.sql", s.value, table)
		}
		if !cols[strings.ToLower(column)] {
			return fmt.Sprintf("column %q: %q has no column %q", s.value, table, column)
		}
	default:
		if !idx.columns[strings.ToLower(column)] && !aliases[strings.ToLower(column)] {
			return fmt.Sprintf("column %q matches no column in sql/schema.sql and no result alias in sql/queries", s.value)
		}
	}
	return ""
}

// readSelectors walks the sqlc config and collects every override
// selector together with the line it is written on, so a failure can name
// the place to fix rather than only the value.
func readSelectors(path string) ([]selector, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", path)
	}
	var out []selector
	for _, block := range seqItems(mapValue(doc.Content[0], "sql")) {
		overrides := mapValue(mapValue(mapValue(block, "gen"), "go"), "overrides")
		for _, entry := range seqItems(overrides) {
			for _, kind := range []string{"column", "db_type"} {
				if v := mapValue(entry, kind); v != nil && v.Value != "" {
					out = append(out, selector{kind: kind, value: v.Value, line: v.Line})
				}
			}
		}
	}
	return out, nil
}

// mapValue returns the value node for key in a YAML mapping, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// seqItems returns the elements of a YAML sequence, or nothing.
func seqItems(node *yaml.Node) []*yaml.Node {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	return node.Content
}

// ----- schema -----

// schemaIndex is what the schema offers a selector: the columns of each
// relation, every column name regardless of relation, and every column
// type in use.
type schemaIndex struct {
	relations map[string]map[string]bool
	columns   map[string]bool
	dbTypes   map[string]bool
}

// Line prefixes that introduce a table constraint rather than a column.
var constraintKeywords = map[string]bool{
	"PRIMARY":    true,
	"UNIQUE":     true,
	"KEY":        true,
	"INDEX":      true,
	"CONSTRAINT": true,
	"FOREIGN":    true,
	"FULLTEXT":   true,
	"SPATIAL":    true,
	"CHECK":      true,
}

func indexSchema(path string) (*schemaIndex, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := stripComments(string(raw))
	idx := &schemaIndex{
		relations: map[string]map[string]bool{},
		columns:   map[string]bool{},
		dbTypes:   map[string]bool{},
	}

	offset := 0
	for _, line := range strings.Split(text, "\n") {
		start := offset
		offset += len(line) + 1

		head := strings.ToUpper(strings.TrimLeft(line, " \t"))
		switch {
		case strings.HasPrefix(head, "CREATE TABLE "):
			name, cols, types := parseTable(text, start)
			if name == "" {
				continue
			}
			idx.add(name, cols)
			for _, t := range types {
				idx.dbTypes[strings.ToLower(t)] = true
			}
		case strings.HasPrefix(head, "CREATE ") && wordAt(head, "VIEW"):
			name, cols := parseView(line, text, start)
			if name == "" {
				continue
			}
			idx.add(name, cols)
		}
	}
	return idx, nil
}

func (idx *schemaIndex) add(relation string, columns []string) {
	rel := strings.ToLower(relation)
	if idx.relations[rel] == nil {
		idx.relations[rel] = map[string]bool{}
	}
	for _, c := range columns {
		lower := strings.ToLower(c)
		idx.relations[rel][lower] = true
		idx.columns[lower] = true
	}
}

// parseTable reads the parenthesised body of a CREATE TABLE beginning at
// start and returns its name, its column names and their declared types.
func parseTable(text string, start int) (name string, columns, types []string) {
	header := lineAt(text, start)
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return "", nil, nil
	}
	name = unquote(strings.TrimSuffix(fields[2], "("))

	open := indexAtDepth(text, start, '(', 0)
	if open < 0 {
		return "", nil, nil
	}
	closing := matchParen(text, open)
	if closing < 0 {
		return "", nil, nil
	}
	for _, item := range splitTopLevel(text[open+1 : closing]) {
		f := strings.Fields(item)
		if len(f) < 2 {
			continue
		}
		if constraintKeywords[strings.ToUpper(f[0])] {
			continue
		}
		col := unquote(f[0])
		if !isIdentifier(col) {
			continue
		}
		columns = append(columns, col)
		types = append(types, baseType(f[1]))
	}
	return name, columns, types
}

// parseView reads the select list of a CREATE VIEW beginning at start and
// returns its name and the column names it publishes.
//
// The select list of the first SELECT is what names the view's columns;
// the branches of a UNION contribute rows, not names.
func parseView(header, text string, start int) (name string, columns []string) {
	fields := strings.Fields(header)
	for i, f := range fields {
		if strings.EqualFold(f, "VIEW") && i+1 < len(fields) {
			name = unquote(fields[i+1])
			break
		}
	}
	if name == "" {
		return "", nil
	}
	sel := indexWordAtDepth(text, start, "SELECT", 0)
	if sel < 0 {
		return name, nil
	}
	from := indexWordAtDepth(text, sel+len("SELECT"), "FROM", 0)
	if from < 0 {
		return name, nil
	}
	for _, item := range splitTopLevel(text[sel+len("SELECT") : from]) {
		if col := resultName(item); col != "" {
			columns = append(columns, col)
		}
	}
	return name, columns
}

// resultName is the column name a select-list item publishes: its alias
// when it has one, otherwise the last segment of the reference. An
// expression with neither is skipped rather than guessed at.
func resultName(item string) string {
	item = strings.TrimSpace(item)
	if item == "" {
		return ""
	}
	if as := indexWordAtDepth(item, 0, "AS", 0); as >= 0 {
		fields := strings.Fields(item[as+len("AS"):])
		if len(fields) == 0 {
			return ""
		}
		alias := unquote(fields[0])
		if isIdentifier(alias) {
			return alias
		}
		return ""
	}
	fields := strings.Fields(item)
	last := unquote(fields[len(fields)-1])
	if i := strings.LastIndex(last, "."); i >= 0 {
		last = unquote(last[i+1:])
	}
	if isIdentifier(last) {
		return last
	}
	return ""
}

// ----- queries -----

// SQL type names that follow AS inside a cast. They are not aliases, and
// admitting them would widen the set of names a selector can claim to
// match for no reason.
var castTypes = map[string]bool{
	"binary": true, "char": true, "date": true, "datetime": true,
	"decimal": true, "double": true, "float": true, "json": true,
	"nchar": true, "real": true, "signed": true, "time": true,
	"unsigned": true,
}

// queryAliases collects every result alias written in the query files.
//
// Aliases are collected at any nesting depth: a CTE's select list is
// inside parentheses, and columns like agent_public_id are named there and
// nowhere else.
func queryAliases(dir string) (map[string]bool, error) {
	aliases := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := stripComments(string(raw))
		for i := 0; ; {
			at := indexWord(text, i, "AS")
			if at < 0 {
				break
			}
			i = at + len("AS")
			fields := strings.Fields(text[i:])
			if len(fields) == 0 {
				continue
			}
			alias := strings.ToLower(unquote(strings.TrimSuffix(fields[0], ",")))
			if isIdentifier(alias) && !castTypes[alias] {
				aliases[alias] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return aliases, nil
}

// ----- SQL text helpers -----

// stripComments removes -- line comments and /* */ block comments,
// leaving quoted text alone and keeping the line structure intact.
//
// Block comments matter more than they look: the table documentation in
// the schema is written in them, and prose contains apostrophes and
// unbalanced parentheses. Left in place they derail the parenthesis
// matching that finds a CREATE TABLE body, and the whole table quietly
// disappears from the index — which turns every override selector naming
// one of its columns into a false report.
func stripComments(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	quote := byte(0)
	inLine := false
	inBlock := false
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

// lineAt returns the line of text that starts at start.
func lineAt(text string, start int) string {
	if end := strings.IndexByte(text[start:], '\n'); end >= 0 {
		return text[start : start+end]
	}
	return text[start:]
}

// indexAtDepth finds the first occurrence of c at the given parenthesis
// depth, ignoring quoted text.
func indexAtDepth(text string, from int, c byte, depth int) int {
	d := 0
	quote := byte(0)
	for i := from; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
			continue
		}
		if ch == c && d == depth {
			return i
		}
		switch ch {
		case '(':
			d++
		case ')':
			d--
		}
	}
	return -1
}

// matchParen returns the index of the ')' closing the '(' at open.
func matchParen(text string, open int) int {
	d := 0
	quote := byte(0)
	for i := open; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			d++
		case ')':
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits a comma-separated list on the commas that are not
// inside parentheses or quotes.
func splitTopLevel(text string) []string {
	var out []string
	d := 0
	quote := byte(0)
	start := 0
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			d++
		case ')':
			d--
		case ',':
			if d == 0 {
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

// indexWord finds the next occurrence of word as a whole word, at any
// depth, ignoring quoted text and case.
func indexWord(text string, from int, word string) int {
	return findWord(text, from, word, -1)
}

// indexWordAtDepth finds the next occurrence of word as a whole word at
// exactly the given parenthesis depth.
func indexWordAtDepth(text string, from int, word string, depth int) int {
	return findWord(text, from, word, depth)
}

// findWord scans for a whole-word, case-insensitive match. A depth below
// zero means any depth.
func findWord(text string, from int, word string, depth int) int {
	d := 0
	quote := byte(0)
	for i := from; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
			continue
		case '(':
			d++
			continue
		case ')':
			d--
			continue
		}
		if depth >= 0 && d != depth {
			continue
		}
		if i+len(word) > len(text) {
			return -1
		}
		if !strings.EqualFold(text[i:i+len(word)], word) {
			continue
		}
		if i > 0 && isIdentifierByte(text[i-1]) {
			continue
		}
		if i+len(word) < len(text) && isIdentifierByte(text[i+len(word)]) {
			continue
		}
		return i
	}
	return -1
}

// wordAt reports whether word appears as a whole word in text.
func wordAt(text, word string) bool {
	return findWord(text, 0, word, -1) >= 0
}

// baseType is the type name without its length or precision, e.g.
// "VARCHAR(64)" -> "VARCHAR".
func baseType(decl string) string {
	if i := strings.IndexByte(decl, '('); i >= 0 {
		return decl[:i]
	}
	return decl
}

// unquote strips the backticks or quotes an identifier may be written in.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []string{"`", `"`, "'"} {
		s = strings.Trim(s, q)
	}
	return s
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentifierByte(s[i]) {
			return false
		}
	}
	return true
}

func isIdentifierByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// ----- paths -----

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "sql", "sqlc.yaml")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "check-sqlc-overrides.go")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("check-sqlc-overrides: could not locate repository root from %s", wd)
		}
		dir = parent
	}
}

// relPath renders a path relative to the repository root, falling back to
// the absolute path when it is outside.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
