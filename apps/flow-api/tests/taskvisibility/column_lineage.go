package taskvisibility

import (
	"regexp"
	"strings"
)

// ----------------------------------------------------------------------
// Subqueries
// ----------------------------------------------------------------------
//
// A derived table and a CTE are views written inside the statement, and a
// title projected through one reaches the reader exactly as a title
// projected from `tasks` does. They are resolved here the way
// [resolveView] resolves a view, with one difference that matters: the
// result records which alias inside the subquery the content came from,
// because the wrapper exposes none of the predicate's inputs and the rule
// would have to be written against the inner alias.
//
// Not everything can be followed. What cannot is said so — see [Opaque] —
// rather than left out, which is the whole difference between this and
// what was here before: `SELECT title, ... FROM ( ... UNION ... ) linked`
// found several relations, decided none of them was the one a bare column
// belonged to, dropped every column, and reported a statement projecting
// two task titles as projecting nothing at all.

// maxSubqueryDepth bounds how far this follows a statement into itself. A
// projection nested deeper is reported as [Opaque], never dropped.
const maxSubqueryDepth = 8

// columnRef is one content column of a relation, named by the alias that
// reads it in the statement it is read in.
type columnRef struct {
	alias  string
	source *Source
	column string
}

// binding is what an alias refers to: a relation from the schema, a
// subquery written into the statement, or something that is neither.
type binding struct {
	source *Source
	sub    *subquery
}

// subquery is a resolved SELECT written inside a statement.
type subquery struct {
	// named maps a column name the subquery exposes onto the content
	// behind it.
	named map[string][]columnRef
	// unnamed is content it exposes under no name a caller can write, so
	// only a `*` over the subquery reaches it.
	unnamed []columnRef
	// opaqueNames are the exposed names whose text could not be followed;
	// opaqueStar says the same of an entry that has no name of its own.
	opaqueNames map[string]bool
	opaqueStar  bool
}

// refs returns the content a column of the subquery carries, and whether
// that column could be followed at all.
func (q *subquery) refs(column string) ([]columnRef, bool) {
	if q == nil {
		return nil, true
	}
	if column == "*" {
		out := append([]columnRef{}, q.unnamed...)
		for _, refs := range q.named {
			out = append(out, refs...)
		}
		// A `*` reaches every entry, including the ones nobody could
		// follow, so it inherits their answer.
		return out, !q.opaqueStar && len(q.opaqueNames) == 0
	}
	if q.opaqueNames[column] {
		return nil, false
	}
	return q.named[column], true
}

// scope is what names mean inside one statement: the relations the schema
// defines, plus the CTEs the statement declares in front of itself.
type scope struct {
	sources map[string]*Source
	ctes    map[string]*subquery
}

func newScope(sources map[string]*Source) scope {
	return scope{sources: sources, ctes: map[string]*subquery{}}
}

var withPrefixPat = regexp.MustCompile(`(?is)^\s*with\s+(recursive\s+)?`)

// takeCTEs resolves the statement's WITH clause into the scope and
// returns the statement that follows it.
//
// The CTEs are resolved in the order they are written, so one that reads
// an earlier one is followed. A recursive CTE reading itself is not: at
// the point its own body is read it is not resolved yet, and the branch
// referring to it binds to nothing. The seed branch is where content
// enters such a CTE, and that branch is read.
func (sc scope) takeCTEs(sql string, depth int) string {
	loc := withPrefixPat.FindStringIndex(sql)
	if loc == nil {
		return sql
	}
	rest := sql[loc[1]:]
	for {
		name, after, ok := readIdentifier(rest)
		if !ok {
			return rest
		}
		after = strings.TrimLeft(after, " \t\r\n")
		// An optional column-name list stands between the name and AS.
		if strings.HasPrefix(after, "(") {
			end := matchParen(after, 0)
			if end < 0 {
				return rest
			}
			after = strings.TrimLeft(after[end+1:], " \t\r\n")
		}
		if !strings.HasPrefix(strings.ToLower(after), "as") {
			return rest
		}
		after = strings.TrimLeft(after[len("as"):], " \t\r\n")
		if !strings.HasPrefix(after, "(") {
			return rest
		}
		end := matchParen(after, 0)
		if end < 0 {
			return rest
		}
		sc.ctes[strings.ToLower(name)] = sc.resolve(after[1:end], depth+1)
		after = strings.TrimLeft(after[end+1:], " \t\r\n")
		if !strings.HasPrefix(after, ",") {
			return after
		}
		rest = after[1:]
	}
}

// resolve reads one subquery body — every branch of it — into the
// content its columns carry.
func (sc scope) resolve(body string, depth int) *subquery {
	out := &subquery{named: map[string][]columnRef{}, opaqueNames: map[string]bool{}}
	if depth > maxSubqueryDepth {
		out.opaqueStar = true
		return out
	}
	for _, branch := range unionBranches(body) {
		inner := scope{sources: sc.sources, ctes: sc.ctes}
		branch = inner.takeCTEs(branch, depth)
		binds := inner.bindingsIn(branch, depth)
		sole := soleBinding(binds)
		for _, item := range selectItems(topLevelSelectList(branch)) {
			refs, followed := inner.itemRefs(item, binds, sole, depth)
			name := itemOutputName(item)
			if !followed {
				if name == "" {
					out.opaqueStar = true
				} else {
					out.opaqueNames[name] = true
				}
				continue
			}
			if len(refs) == 0 {
				continue
			}
			if name == "" {
				out.unnamed = append(out.unnamed, refs...)
				continue
			}
			out.named[name] = append(out.named[name], refs...)
		}
	}
	return out
}

// bindingsIn maps every alias the statement binds at its own level onto
// what it refers to.
//
// Only that level is read. A relation named inside a subquery is that
// subquery's, and reading it here is what made a statement over a derived
// table look like a statement over three relations — with the effect that
// no unqualified column in it belonged to anything.
func (sc scope) bindingsIn(sql string, depth int) map[string]binding {
	out := map[string]binding{}
	text := sql
	for _, d := range derivedTables(sql) {
		out[d.alias] = binding{sub: sc.resolve(d.body, depth+1)}
		text = blankSpan(text, d.start, d.end)
	}
	for _, m := range relationPat.FindAllStringSubmatch(blankNested(text), -1) {
		rel := strings.ToLower(m[2])
		alias := strings.ToLower(m[5])
		if alias == "" || reservedAfterRelation[alias] {
			alias = rel
		}
		switch {
		case sc.ctes[rel] != nil:
			out[alias] = binding{sub: sc.ctes[rel]}
		case sc.sources[rel] != nil:
			out[alias] = binding{source: sc.sources[rel]}
		default:
			// Bound to something that exposes no task content. Recorded
			// anyway: it is what makes an unqualified column ambiguous.
			out[alias] = binding{}
		}
	}
	return out
}

// itemRefs returns the content one select-list entry puts on the wire,
// and whether the entry could be followed at all.
func (sc scope) itemRefs(item string, binds map[string]binding, sole string, depth int) ([]columnRef, bool) {
	if alias, column, _, ok := selectItemColumn(item); ok {
		return refsThrough(binds, alias, column)
	}
	if column, _, ok := bareSelectItemColumn(item); ok {
		// A bare column belongs to the one relation the statement reads.
		// Leaving it out entirely would put a projection outside the rule
		// for spelling `SELECT title FROM tasks` rather than
		// `SELECT t.title FROM tasks t`, which is a difference in typing
		// and not in what reaches the reader.
		if sole != "" {
			return refsThrough(binds, sole, column)
		}
		// More than one relation and no qualifier: which one it came from
		// is a guess, and a check about disclosure does not guess. It says
		// so instead, and only where the guess would matter — a name no
		// bound relation exposes as content discloses nothing whichever
		// one it came from.
		return nil, !namesContent(binds, column)
	}
	return sc.expressionRefs(item, binds, sole, depth)
}

// expressionRefs reads the content an expression puts on the wire.
//
// An expression is followed as far as the columns it names: every
// `alias.column` in it is resolved the way a plain projection would be,
// which is what lets `BIN_TO_UUID(t.public_id, 0)` be seen for what it is
// rather than assumed to carry a title.
//
// A statement written into the expression — a scalar subquery, an EXISTS —
// is resolved as a subquery, so `(SELECT t.title FROM tasks t WHERE ...)`
// is followed to the same place a plain projection of it would be. What
// is left over after both are read is an expression whose text names no
// relation this could resolve, and that is the case that says so.
func (sc scope) expressionRefs(item string, binds map[string]binding, sole string, depth int) ([]columnRef, bool) {
	var out []columnRef
	rest := item
	for _, nested := range nestedSelects(item) {
		refs, followed := sc.resolve(nested.body, depth+1).refs("*")
		if !followed {
			return nil, false
		}
		out = append(out, refs...)
		rest = blankSpan(rest, nested.start, nested.end)
	}
	if containsSelect(rest) {
		// A SELECT this could not cut out of the entry: the statement the
		// column comes from is not one this read.
		return nil, false
	}
	body := stripOutputAlias(stripLiterals(rest))
	for _, m := range qualifiedRefPat.FindAllStringSubmatch(body, -1) {
		refs, followed := refsThrough(binds, strings.ToLower(m[1]), strings.ToLower(m[2]))
		if !followed {
			return nil, false
		}
		out = append(out, refs...)
	}
	if len(out) > 0 || sole == "" {
		return out, true
	}
	// No qualifier anywhere and one relation to belong to: the bare names
	// in the expression are that relation's.
	src := binds[sole].source
	if src == nil {
		return nil, true
	}
	for _, word := range identifierPat.FindAllString(body, -1) {
		if _, isContent := src.Content[strings.ToLower(word)]; isContent {
			out = append(out, columnRef{alias: sole, source: src, column: strings.ToLower(word)})
		}
	}
	return out, true
}

// refsThrough resolves one `alias.column` against the statement's
// bindings.
func refsThrough(binds map[string]binding, alias, column string) ([]columnRef, bool) {
	bound, ok := binds[alias]
	if !ok {
		return nil, true
	}
	if bound.sub != nil {
		return bound.sub.refs(column)
	}
	if bound.source == nil {
		return nil, true
	}
	if column == "*" {
		out := make([]columnRef, 0, len(bound.source.Content))
		for name := range bound.source.Content {
			out = append(out, columnRef{alias: alias, source: bound.source, column: name})
		}
		return out, true
	}
	if _, isContent := bound.source.Content[column]; !isContent {
		return nil, true
	}
	return []columnRef{{alias: alias, source: bound.source, column: column}}, true
}

// namesContent reports whether any relation the statement reads exposes
// content under this column name.
func namesContent(binds map[string]binding, column string) bool {
	for _, bound := range binds {
		switch {
		case bound.source != nil:
			if _, ok := bound.source.Content[column]; ok {
				return true
			}
		case bound.sub != nil:
			if refs, _ := bound.sub.refs(column); len(refs) > 0 {
				return true
			}
		}
	}
	return false
}

// soleBinding returns the alias a statement reads when it reads exactly
// one relation, and the empty string otherwise.
//
// An unaliased relation binds under its own name, so a statement reading
// one table twice under two aliases has two bindings and no sole
// relation — which is the case where a bare column could belong to
// either.
func soleBinding(binds map[string]binding) string {
	if len(binds) != 1 {
		return ""
	}
	for alias := range binds {
		return alias
	}
	return ""
}

// derivedTable is one SELECT in a statement's FROM clause, bound to an
// alias. start is the FROM, JOIN or comma that introduces it and end is
// just past its alias, so the whole of it can be taken out of the text
// the plain relation scan then reads.
type derivedTable struct {
	alias      string
	body       string
	start, end int
}

// derivedTables finds the subqueries a statement's own FROM clause reads
// from, at that level only.
//
// The search starts at the top-level FROM so that a subquery in the
// select list or in a WHERE predicate is not mistaken for a relation the
// statement reads: those return values, not rows to join against.
func derivedTables(sql string) []derivedTable {
	lower := strings.ToLower(sql)
	from := topLevelFrom(lower)
	if from < 0 {
		return nil
	}
	var out []derivedTable
	depth := 0
	for i := from; i < len(sql); i++ {
		switch lower[i] {
		case '\'':
			i = skipString(lower, i)
		case '(':
			if keyword, opens := derivedTableStart(lower, i); depth == 0 && opens {
				end := matchParen(sql, i)
				if end < 0 {
					return out
				}
				alias, after := readTableAlias(sql, end+1)
				if alias != "" {
					out = append(out, derivedTable{
						alias: alias, body: sql[i+1 : end], start: keyword, end: after,
					})
				}
				i = end
				continue
			}
			depth++
		case ')':
			depth--
		}
	}
	return out
}

// derivedTableStart reports whether the parenthesis at `at` opens a
// relation the FROM clause reads rather than a grouped expression — what
// stands in front of it is FROM, JOIN, or the comma between two
// relations — and returns the offset of that word.
//
// The word is part of what the derived table occupies, because the plain
// relation scan runs over the same text afterwards: leaving a bare FROM
// behind made it read the next word as the relation, which for a
// statement ending `) linked ORDER BY ...` is the word ORDER. That second
// binding was enough to make every unqualified column in the statement
// ambiguous.
func derivedTableStart(lower string, at int) (int, bool) {
	head := strings.TrimRight(lower[:at], " \t\r\n")
	if strings.HasSuffix(head, ",") {
		return len(head) - 1, true
	}
	for _, word := range []string{"straight_join", "from", "join"} {
		if strings.HasSuffix(head, word) && isBoundary(head, len(head)-len(word), len(word)) {
			return len(head) - len(word), true
		}
	}
	return 0, false
}

// readTableAlias reads the name a derived table is bound to, with or
// without AS, and returns it with the offset just past it.
func readTableAlias(sql string, at int) (string, int) {
	rest := strings.TrimLeft(sql[at:], " \t\r\n")
	at = len(sql) - len(rest)
	if len(rest) >= 2 && strings.EqualFold(rest[:2], "as") && (len(rest) == 2 || !isIdentByte(rest[2])) {
		rest = strings.TrimLeft(rest[2:], " \t\r\n")
		at = len(sql) - len(rest)
	}
	name, after, ok := readIdentifier(rest)
	if !ok {
		return "", at
	}
	return strings.ToLower(name), at + (len(rest) - len(after))
}

// readIdentifier reads a leading identifier, backquoted or not, and
// returns it with the text that follows.
func readIdentifier(text string) (string, string, bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "`") {
		if end := strings.Index(trimmed[1:], "`"); end >= 0 {
			return trimmed[1 : end+1], trimmed[end+2:], true
		}
		return "", text, false
	}
	i := 0
	for i < len(trimmed) && isIdentByte(trimmed[i]) {
		i++
	}
	if i == 0 {
		return "", text, false
	}
	return trimmed[:i], trimmed[i:], true
}

// unionBranches splits a subquery body on the UNIONs between its
// branches, ignoring any inside parentheses or string literals.
func unionBranches(body string) []string {
	lower := strings.ToLower(body)
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "union") && isBoundary(lower, i, len("union")):
			out = append(out, body[start:i])
			rest := strings.TrimLeft(lower[i+len("union"):], " \t\r\n")
			skip := len(lower[i+len("union"):]) - len(rest)
			if strings.HasPrefix(rest, "all") && isBoundary(rest, 0, len("all")) {
				skip += len("all")
			}
			i += len("union") + skip - 1
			start = i + 1
		}
	}
	return append(out, body[start:])
}

// nestedSelect is a parenthesised statement inside a select-list entry:
// a scalar subquery, the argument of an EXISTS, the right-hand side of an
// IN.
type nestedSelect struct {
	body       string
	start, end int
}

// nestedSelects finds the statements written into one select-list entry.
//
// A parenthesis is a statement when what follows it is SELECT; anything
// else — a function's arguments, a grouped expression — is descended into
// rather than taken whole, so a COALESCE around a scalar subquery yields
// the subquery and not the call. A subquery inside a subquery is left to
// the resolution of the one that contains it.
func nestedSelects(item string) []nestedSelect {
	lower := strings.ToLower(item)
	var out []nestedSelect
	for i := 0; i < len(item); i++ {
		switch lower[i] {
		case '\'':
			i = skipString(lower, i)
		case '(':
			end := matchParen(item, i)
			if end < 0 {
				return out
			}
			inner := item[i+1 : end]
			if strings.HasPrefix(strings.TrimSpace(strings.ToLower(inner)), "select") {
				out = append(out, nestedSelect{body: inner, start: i, end: end + 1})
				i = end
			}
		}
	}
	return out
}

// matchParen returns the offset of the parenthesis closing the one at
// open, or -1 when it is never closed.
func matchParen(sql string, open int) int {
	depth := 0
	for i := open; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			i = skipString(sql, i)
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

// topLevelFrom returns the offset of the FROM that ends a statement's
// select list, or -1.
func topLevelFrom(lower string) int {
	start := strings.Index(lower, "select")
	if start < 0 {
		return -1
	}
	depth := 0
	for i := start + len("select"); i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "from") && isBoundary(lower, i, len("from")):
			return i
		}
	}
	return -1
}

// blankSpan replaces a region with spaces, keeping every other offset
// where it was.
func blankSpan(sql string, start, end int) string {
	if start < 0 || end > len(sql) || start >= end {
		return sql
	}
	return sql[:start] + strings.Repeat(" ", end-start) + sql[end:]
}

// blankNested replaces everything inside parentheses with spaces, so a
// relation named in a subquery is not read as one this statement joins.
func blankNested(sql string) string {
	out := []byte(sql)
	depth := 0
	for i := 0; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			end := skipString(sql, i)
			if depth > 0 {
				for j := i; j <= end && j < len(out); j++ {
					out[j] = ' '
				}
			}
			i = end
			continue
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth > 0 || (depth == 0 && sql[i] == ')') {
			out[i] = ' '
		}
	}
	return string(out)
}

var (
	qualifiedRefPat  = regexp.MustCompile("(?is)`?(\\w+)`?\\s*\\.\\s*`?(\\w+)`?")
	identifierPat    = regexp.MustCompile(`(?i)[a-z_][a-z0-9_]*`)
	selectWordPat    = regexp.MustCompile(`(?is)\bselect\b`)
	outputAliasPat   = regexp.MustCompile("(?is)\\s+as\\s+`?(\\w+)`?\\s*$")
	stringLiteralPat = regexp.MustCompile(`'(\\.|[^'\\])*'`)
)

// containsSelect reports whether a select-list entry wraps a statement of
// its own.
func containsSelect(item string) bool { return selectWordPat.MatchString(item) }

// stripLiterals blanks string literals so a word inside one is not read
// as a column name.
func stripLiterals(item string) string {
	return stringLiteralPat.ReplaceAllStringFunc(item, func(lit string) string {
		return strings.Repeat(" ", len(lit))
	})
}

// stripOutputAlias removes the `AS name` a select-list entry ends with,
// so the name it is exposed under is not read as a column it reads.
func stripOutputAlias(item string) string {
	return outputAliasPat.ReplaceAllString(item, "")
}

// itemOutputName returns the name a select-list entry is exposed under,
// or the empty string when it has none a caller could write.
func itemOutputName(item string) string {
	if _, _, exposed, ok := selectItemColumn(item); ok {
		return exposed
	}
	if _, exposed, ok := bareSelectItemColumn(item); ok {
		return exposed
	}
	if m := outputAliasPat.FindStringSubmatch(item); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// ----------------------------------------------------------------------
// SQL shredding
// ----------------------------------------------------------------------

var relationPat = regexp.MustCompile(`(?is)\b(from|join)\s+` + "`?" + `(\w+)` + "`?" + `((\s+as)?\s+` + "`?" + `(\w+)` + "`?" + `)?`)

// reservedAfterRelation are the words that follow a relation name
// without being an alias for it.
var reservedAfterRelation = map[string]bool{
	"on": true, "where": true, "inner": true, "left": true, "right": true,
	"cross": true, "join": true, "using": true, "group": true, "order": true,
	"limit": true, "set": true, "and": true, "or": true, "for": true,
	"union": true, "having": true, "straight_join": true, "as": true,
	"select": true, "natural": true, "lateral": true,
}

// relationBindings maps every alias in a statement onto the relation it
// names. An unaliased relation binds under its own name, which is how
// `FROM tasks WHERE tasks.title` resolves.
func relationBindings(sql string) map[string]string {
	out := map[string]string{}
	for _, m := range relationPat.FindAllStringSubmatch(sql, -1) {
		rel := strings.ToLower(m[2])
		alias := strings.ToLower(m[5])
		if alias == "" || reservedAfterRelation[alias] {
			alias = rel
		}
		out[alias] = rel
	}
	return out
}

// topLevelSelectList returns the text between a statement's leading
// SELECT and the FROM at the same nesting depth. A correlated subquery
// in the select list is inside parentheses, so its own FROM never ends
// the list.
func topLevelSelectList(sql string) string {
	lower := strings.ToLower(sql)
	start := strings.Index(lower, "select")
	if start < 0 {
		return ""
	}
	start += len("select")
	depth := 0
	for i := start; i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "from") && isBoundary(lower, i, len("from")):
			return sql[start:i]
		}
	}
	return sql[start:]
}

func skipString(text string, i int) int {
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '\'' {
			return i
		}
	}
	return i
}

func isBoundary(text string, at, length int) bool {
	before := at == 0 || !isIdentByte(text[at-1])
	after := at+length >= len(text) || !isIdentByte(text[at+length])
	return before && after
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// selectItems splits a select list on the commas between its entries,
// ignoring commas inside parentheses or string literals.
func selectItems(list string) []string {
	var out []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(list); i++ {
		switch {
		case list[i] == '\'':
			end := skipString(list, i)
			current.WriteString(list[i : end+1])
			i = end
		case list[i] == '(':
			depth++
			current.WriteByte(list[i])
		case list[i] == ')':
			depth--
			current.WriteByte(list[i])
		case list[i] == ',' && depth == 0:
			out = appendItem(out, current.String())
			current.Reset()
		default:
			current.WriteByte(list[i])
		}
	}
	return appendItem(out, current.String())
}

func appendItem(items []string, item string) []string {
	if trimmed := strings.TrimSpace(item); trimmed != "" {
		return append(items, trimmed)
	}
	return items
}

var plainColumnPat = regexp.MustCompile(`(?is)^` + "`?" + `(\w+)` + "`?" + `\s*\.\s*` + "`?" + `(\w+|\*)` + "`?" + `(\s+as\s+` + "`?" + `(\w+)` + "`?" + `)?\s*$`)

var bareColumnPat = regexp.MustCompile(`(?is)^` + "`?" + `(\w+|\*)` + "`?" + `(\s+as\s+` + "`?" + `(\w+)` + "`?" + `)?\s*$`)

// bareSelectItemColumn reads a select-list entry written without a
// qualifier — `title`, or `title AS name`.
func bareSelectItemColumn(item string) (column, exposed string, ok bool) {
	m := bareColumnPat.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", "", false
	}
	column = strings.ToLower(m[1])
	exposed = column
	if m[3] != "" {
		exposed = strings.ToLower(m[3])
	}
	return column, exposed, true
}

// selectItemColumn reads a select-list entry of the form
// `alias.column [AS name]`, returning the alias, the source column and
// the name the row exposes it under.
//
// Anything else — an expression, a literal, a correlated subquery — is
// not a column projection and is reported as such. A view that wraps a
// title in a function is out of what this can follow, and would have to
// be spelled plainly to come into scope; a check that guessed at
// expressions would be guessing about the thing it is meant to prove.
func selectItemColumn(item string) (alias, column, exposed string, ok bool) {
	m := plainColumnPat.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", "", "", false
	}
	alias = strings.ToLower(m[1])
	column = strings.ToLower(m[2])
	exposed = column
	if m[4] != "" {
		exposed = strings.ToLower(m[4])
	}
	return alias, column, exposed, true
}
