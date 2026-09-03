// Package softdelete derives, from the committed SQL, which tables need a
// writer that revives a soft-deleted row instead of inserting beside it.
//
// The schema forbids `enabled` inside a unique index, so a table that has
// to keep one row per tuple keys the tuple alone. That choice moves half
// of the rule into the writers: with `UNIQUE (a, b)` and no liveness
// column, a revoked row still occupies the tuple, so inserting the same
// pair again collides with the tombstone and the second grant fails
// forever. The insert has to say `ON DUPLICATE KEY UPDATE ... enabled =
// TRUE`, or the caller has to look the row up including tombstones and
// revive it.
//
// Nothing about that is visible in the table definition, so the schema
// checks cannot see it and the queries were never read by any gate. The
// tables are therefore derived here rather than listed: a table added
// later with the same shape is picked up without anyone remembering to
// register it.
//
// Three things have to be true at once for the failure to be reachable,
// and all three are read out of the SQL:
//
//	shape    a soft-delete flag, plus a unique key naming a tuple that
//	         can recur — references to rows that already exist, and
//	         categories with a fixed domain. A key over a value the
//	         writer mints per row (a token hash, a public_id, a
//	         sequence number) cannot collide with a tombstone, and a
//	         key that includes a liveness marker drops its tombstones
//	         out of the index by design.
//	insert   something writes new rows of the table.
//	revoke   something sets enabled = FALSE on it. Where no statement
//	         does, no tombstone is ever written; user_integrations,
//	         for one, hard-deletes instead.
//
// Only sql/queries is read. A handful of writers live in Go instead
// (memberkit, itemkit, the auto-action executor); those tables are
// covered here through their query-tree writer, and a table written
// solely from Go is outside what this can see.
package softdelete

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// UniqueKey is one unique index on a table.
type UniqueKey struct {
	Name    string
	Columns []string
}

// Table is a table whose uniqueness rule makes a soft-deleted row hold on
// to the tuple it carries, so a writer has to revive it.
type Table struct {
	Name string
	// BusinessKeys are the unique keys over a recurring tuple: only
	// references and categories, no liveness marker to let tombstones
	// leave the index, and nothing the writer mints per row.
	BusinessKeys []UniqueKey
	// LivenessColumns are generated columns derived from `enabled`.
	// A key over one of those scopes itself to live rows and needs no
	// writer support, which is why such keys are not business keys.
	LivenessColumns []string
}

// Query is one named statement in sql/queries.
type Query struct {
	Name string
	Kind string
	Path string
	Body string
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
			return "", errors.New("softdelete: go.work not found above the working directory")
		}
		dir = parent
	}
}

// RevivalTables reads sql/schema.sql under root and returns the tables
// whose writers have to revive rather than insert, ordered by name.
func RevivalTables(root string) ([]Table, error) {
	raw, err := os.ReadFile(filepath.Join(root, "sql", "schema.sql")) //#nosec G304 -- repository path
	if err != nil {
		return nil, err
	}
	var tables []Table
	for _, def := range parseCreateTables(string(raw)) {
		table, ok := classify(def)
		if ok {
			tables = append(tables, table)
		}
	}
	return tables, nil
}

// Queries reads every statement under sql/queries, in file order.
func Queries(root string) ([]Query, error) {
	queriesDir := filepath.Join(root, "sql", "queries")
	var out []Query
	err := filepath.WalkDir(queriesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, splitNamedQueries(rel, string(raw))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// InScope narrows the tables carrying the shape down to the ones where
// the failure is reachable: something inserts rows and something revokes
// them. A table nothing revokes never holds a tombstone for an insert to
// collide with.
func InScope(tables []Table, queries []Query) []Table {
	var out []Table
	for _, table := range tables {
		if len(InsertsInto(queries, table.Name)) == 0 {
			continue
		}
		if len(RevokingStatements(queries, table.Name)) == 0 {
			continue
		}
		out = append(out, table)
	}
	return out
}

// RevivalWriter is the evidence that a table's writers can bring a
// revoked row back rather than collide with it.
type RevivalWriter struct {
	// Upserts are the inserts that re-enable the row they hit.
	Upserts []Query
	// PlainInserts are the inserts that do not.
	PlainInserts []Query
	// Revives are the UPDATE statements that re-enable a row, and
	// Lookups the queries that can find a revoked row by its business
	// key. The two together are the other accepted shape: find the
	// tombstone, then revive it.
	Revives []Query
	Lookups []Query
}

// WriterFor collects that evidence for one table.
func WriterFor(queries []Query, table Table) RevivalWriter {
	writer := RevivalWriter{
		Revives: ReviveStatements(queries, table.Name),
		Lookups: TombstoneAwareLookups(queries, table),
	}
	for _, insert := range InsertsInto(queries, table.Name) {
		if RevivesOnConflict(insert) {
			writer.Upserts = append(writer.Upserts, insert)
			continue
		}
		writer.PlainInserts = append(writer.PlainInserts, insert)
	}
	return writer
}

// Satisfied reports whether the table's writers meet the contract:
// every insert re-enables on conflict, or a find-then-revive pair exists
// to reach the tombstone before an insert can hit it.
func (w RevivalWriter) Satisfied() bool {
	if len(w.Revives) > 0 && len(w.Lookups) > 0 {
		return true
	}
	return len(w.PlainInserts) == 0 && len(w.Upserts) > 0
}

// InsertsInto reports the queries that insert into table.
func InsertsInto(queries []Query, table string) []Query {
	var out []Query
	for _, q := range queries {
		if matchesStatement(q.Body, "INSERT INTO", table) {
			out = append(out, q)
		}
	}
	return out
}

// RevivesOnConflict reports whether an insert re-enables the row it
// collided with, which is the whole point of keying the tuple alone.
func RevivesOnConflict(q Query) bool {
	body := normalize(q.Body)
	idx := strings.Index(body, "on duplicate key update")
	if idx < 0 {
		return false
	}
	return strings.Contains(body[idx:], "enabled = true")
}

// ReviveStatements reports the queries that bring a row of table back
// into service: an UPDATE whose SET clause writes enabled = TRUE.
func ReviveStatements(queries []Query, table string) []Query {
	return updatesAssigning(queries, table, "enabled = true")
}

// RevokingStatements reports the queries that revoke a row of table.
// Without one of these no tombstone is ever written, so the tuple can
// never be blocked and the insert has nothing to revive.
func RevokingStatements(queries []Query, table string) []Query {
	return updatesAssigning(queries, table, "enabled = false")
}

func updatesAssigning(queries []Query, table, assignment string) []Query {
	var out []Query
	for _, q := range queries {
		if !matchesStatement(q.Body, "UPDATE", table) {
			continue
		}
		// Only the SET clause counts. `SET enabled = FALSE WHERE
		// enabled = TRUE` is a revocation, and reading the whole
		// statement would call it a revival.
		if strings.Contains(setClause(q.Body), assignment) {
			out = append(out, q)
		}
	}
	return out
}

// setClause returns the assignments of a normalized UPDATE statement.
func setClause(body string) string {
	text := normalize(body)
	start := strings.Index(text, " set ")
	if start < 0 {
		return ""
	}
	rest := text[start+len(" set "):]
	if end := strings.Index(rest, " where "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TombstoneAwareLookups reports the queries that can find a revoked row
// of table by its business key: a single-table SELECT that names every
// column of one key and never restricts on enabled. Without one, the
// revive statement is unreachable — the caller looks for a live row,
// finds nothing, inserts, and collides with the tombstone it did not see.
func TombstoneAwareLookups(queries []Query, table Table) []Query {
	var out []Query
	for _, q := range queries {
		body := normalize(q.Body)
		if !strings.HasPrefix(body, " select ") {
			continue
		}
		if !matchesStatement(q.Body, "FROM", table.Name) {
			continue
		}
		if strings.Contains(body, " join ") {
			continue
		}
		at := strings.Index(body, " where ")
		if at < 0 {
			continue
		}
		// Only the predicate matters: selecting the enabled column is
		// how a lookup reports the state it found, and is the opposite
		// of filtering it away.
		where := body[at:]
		if strings.Contains(where, "enabled") {
			continue
		}
		for _, key := range table.BusinessKeys {
			if mentionsAll(where, key.Columns) {
				out = append(out, q)
				break
			}
		}
	}
	return out
}

func mentionsAll(text string, columns []string) bool {
	for _, column := range columns {
		if !strings.Contains(text, column) {
			return false
		}
	}
	return len(columns) > 0
}

// ---------------------------------------------------------------------
// Schema parsing
// ---------------------------------------------------------------------

type tableDef struct {
	name  string
	items []string
}

// parseCreateTables splits every CREATE TABLE body into its top-level
// comma-separated items. It tracks string literals and comments, because
// column comments in this schema hold both parentheses and the `--`
// sequence, and a line-oriented split reads those as structure.
func parseCreateTables(text string) []tableDef {
	const marker = "CREATE TABLE "
	var defs []tableDef
	for offset := 0; ; {
		idx := strings.Index(text[offset:], marker)
		if idx < 0 {
			return defs
		}
		start := offset + idx + len(marker)
		open := strings.Index(text[start:], "(")
		if open < 0 {
			return defs
		}
		name := strings.Trim(strings.TrimSpace(text[start:start+open]), "`")
		bodyStart := start + open + 1
		bodyEnd := matchingParen(text, bodyStart)
		if bodyEnd < 0 {
			return defs
		}
		defs = append(defs, tableDef{name: name, items: splitTopLevel(text[bodyStart:bodyEnd])})
		offset = bodyEnd
	}
}

// matchingParen returns the index of the ")" closing the "(" that ends
// just before start, or -1.
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

// splitTopLevel splits a table body on the commas that separate its
// definitions, dropping comments and ignoring commas inside strings or
// nested parentheses.
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
			if body[i] == '(' {
				depth++
			}
			if body[i] == ')' {
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
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return items
	}
	return append(items, trimmed)
}

// indexDefinitions are the table items that are not columns.
var indexDefinitions = []string{
	"PRIMARY KEY", "UNIQUE KEY", "UNIQUE INDEX", "KEY ", "INDEX ",
	"FULLTEXT", "SPATIAL", "CONSTRAINT", "CHECK ",
}

// classify decides whether a table carries the shape that needs a
// revival writer: a soft-delete flag, plus a unique key over a tuple
// that can legitimately be formed again after it was revoked.
func classify(def tableDef) (Table, bool) {
	table := Table{Name: def.name}
	columns := map[string]string{}
	var uniques []UniqueKey
	referenced := map[string]bool{}
	for _, item := range def.items {
		upper := strings.ToUpper(item)
		if strings.HasPrefix(upper, "UNIQUE") {
			if key, ok := parseKey(item); ok {
				uniques = append(uniques, key)
			}
			continue
		}
		if strings.Contains(upper, "FOREIGN KEY") {
			for _, column := range foreignKeyColumns(item) {
				referenced[column] = true
			}
			continue
		}
		if hasAnyPrefix(upper, indexDefinitions) {
			continue
		}
		name := strings.Trim(strings.Fields(item)[0], "`")
		columns[name] = item
	}
	if _, ok := columns["enabled"]; !ok {
		return Table{}, false
	}
	liveness := map[string]bool{}
	for name, definition := range columns {
		expr, generated := generationExpression(definition)
		if !generated {
			continue
		}
		if strings.Contains(strings.ToLower(expr), "enabled") {
			liveness[name] = true
			table.LivenessColumns = append(table.LivenessColumns, name)
			continue
		}
		// A generated column that projects a foreign key stands in
		// for it in the index — calendar_event_attendees keys on
		// IFNULL(event_id, 0) rather than on event_id itself.
		for source := range referenced {
			if mentionsColumn(expr, source) {
				referenced[name] = true
			}
		}
	}
	categorical := map[string]bool{}
	for name, definition := range columns {
		if strings.HasPrefix(strings.ToUpper(strings.Join(strings.Fields(definition)[1:], " ")), "ENUM(") {
			categorical[name] = true
		}
	}
	for _, key := range uniques {
		if isRecurringTuple(key, liveness, referenced, categorical) {
			table.BusinessKeys = append(table.BusinessKeys, key)
		}
	}
	if len(table.BusinessKeys) == 0 {
		return Table{}, false
	}
	return table, true
}

// isRecurringTuple reports whether a key names something a caller can ask
// for twice: rows that already exist and categories from a fixed set. A
// key holding a value minted per row — a token hash, a public_id, a
// counter — is unique whatever the writer does, and a key holding a
// liveness marker keeps tombstones out of the index by itself.
func isRecurringTuple(key UniqueKey, liveness, referenced, categorical map[string]bool) bool {
	references := 0
	for _, column := range key.Columns {
		switch {
		case liveness[column] || column == "public_id" || column == "id":
			return false
		case referenced[column]:
			references++
		case categorical[column]:
		default:
			return false
		}
	}
	return references > 0
}

// parseKey pulls the name and column list out of a UNIQUE KEY item.
func parseKey(item string) (UniqueKey, bool) {
	columns, rest, ok := parenthesizedList(item)
	if !ok {
		return UniqueKey{}, false
	}
	head := strings.Fields(rest)
	if len(head) == 0 {
		return UniqueKey{}, false
	}
	return UniqueKey{Name: strings.Trim(head[len(head)-1], "`"), Columns: columns}, true
}

// foreignKeyColumns returns the local columns of a FOREIGN KEY item.
func foreignKeyColumns(item string) []string {
	at := strings.Index(strings.ToUpper(item), "FOREIGN KEY")
	if at < 0 {
		return nil
	}
	columns, _, ok := parenthesizedList(item[at:])
	if !ok {
		return nil
	}
	return columns
}

// parenthesizedList splits the first parenthesized column list in item,
// and returns the text before it. The closing parenthesis is found by
// matching rather than by searching backwards: a key may carry a COMMENT
// whose text contains parentheses of its own.
func parenthesizedList(item string) (columns []string, before string, ok bool) {
	open := strings.Index(item, "(")
	if open < 0 {
		return nil, "", false
	}
	closing := matchingParen(item, open+1)
	if closing < 0 {
		return nil, "", false
	}
	for _, column := range strings.Split(item[open+1:closing], ",") {
		field := strings.TrimSpace(column)
		// A prefix index writes the length after the name.
		if cut := strings.IndexAny(field, " ("); cut >= 0 {
			field = field[:cut]
		}
		columns = append(columns, strings.Trim(field, "`"))
	}
	return columns, item[:open], true
}

// mentionsColumn reports whether an expression names exactly this column.
func mentionsColumn(expr, column string) bool {
	lower := strings.ToLower(expr)
	for offset := 0; ; {
		at := strings.Index(lower[offset:], column)
		if at < 0 {
			return false
		}
		start := offset + at
		end := start + len(column)
		beforeOK := start == 0 || !isIdentifierByte(lower[start-1])
		afterOK := end >= len(lower) || !isIdentifierByte(lower[end])
		if beforeOK && afterOK {
			return true
		}
		offset = end
	}
}

// generationExpression returns the body of GENERATED ALWAYS AS (...).
func generationExpression(definition string) (string, bool) {
	upper := strings.ToUpper(definition)
	idx := strings.Index(upper, "GENERATED ALWAYS AS")
	if idx < 0 {
		return "", false
	}
	open := strings.Index(definition[idx:], "(")
	if open < 0 {
		return "", false
	}
	start := idx + open + 1
	end := matchingParen(definition, start)
	if end < 0 {
		return "", false
	}
	return definition[start:end], true
}

func hasAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Query parsing
// ---------------------------------------------------------------------

// splitNamedQueries cuts a query file at its sqlc `-- name:` headers.
func splitNamedQueries(path, text string) []Query {
	var out []Query
	var current *Query
	var body strings.Builder
	flush := func() {
		if current == nil {
			return
		}
		current.Body = body.String()
		out = append(out, *current)
		current = nil
		body.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if header, ok := strings.CutPrefix(trimmed, "-- name:"); ok {
			flush()
			fields := strings.Fields(header)
			next := Query{Path: path}
			if len(fields) > 0 {
				next.Name = fields[0]
			}
			if len(fields) > 1 {
				next.Kind = strings.TrimPrefix(fields[1], ":")
			}
			current = &next
			continue
		}
		if current == nil {
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return out
}

// matchesStatement reports whether body applies keyword to exactly this
// table — `INSERT INTO tasks` must not match `task_actors`, and a
// comment mentioning either must not match at all.
func matchesStatement(body, keyword, table string) bool {
	needle := strings.ToLower(keyword) + " " + table
	text := normalize(body)
	for offset := 0; ; {
		idx := strings.Index(text[offset:], needle)
		if idx < 0 {
			return false
		}
		at := offset + idx
		end := at + len(needle)
		if end >= len(text) || !isIdentifierByte(text[end]) {
			return true
		}
		offset = end
	}
}

func isIdentifierByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// normalize lowercases a statement, drops its comments, and collapses
// whitespace so a clause can be matched without caring how it was
// wrapped.
func normalize(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if code, _, found := strings.Cut(line, "--"); found {
			line = code
		}
		out.WriteString(line)
		out.WriteString(" ")
	}
	return " " + strings.Join(strings.Fields(strings.ToLower(out.String())), " ") + " "
}

// Describe renders a table and its keys for a failure message.
func Describe(table Table) string {
	parts := make([]string, 0, len(table.BusinessKeys))
	for _, key := range table.BusinessKeys {
		parts = append(parts, fmt.Sprintf("%s (%s)", key.Name, strings.Join(key.Columns, ", ")))
	}
	return fmt.Sprintf("%s: %s", table.Name, strings.Join(parts, "; "))
}
