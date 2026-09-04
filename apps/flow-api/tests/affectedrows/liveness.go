package affectedrows

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The removal shape this package derives is "a guarded write onto the
// column the reads filter on". Which column that is, is a convention, and
// the schema carries more than one of them: a boolean flag, and a
// tombstone timestamp whose absence means the row is still there.
//
// A derivation that knows one convention does not fail when a second is
// introduced — it silently stops covering the statements written in the
// new one, while the 404 those statements owe their callers is unchanged.
// So the vocabulary is asserted instead of assumed: the candidates are
// read out of the schema by shape, and every one of them has to be
// classified below. A column whose shape lets it carry liveness and that
// nobody has classified fails the check rather than sitting outside it.

// LivenessRole is what a schema column of the live/not-live shape does to
// the set the reads return.
type LivenessRole string

const (
	// RemovalMarker is a column whose written value takes the row out of
	// that set for good. A guarded UPDATE onto one is a removal, so its
	// affected-row count is the caller's not-found answer.
	RemovalMarker LivenessRole = "removal marker"

	// DerivedMarker is a generated column computed from a removal marker.
	// No statement can assign it, so it cannot carry a removal of its
	// own; it exists to scope an index to the live rows.
	DerivedMarker LivenessRole = "derived from a removal marker"

	// ReversibleState is a state a row moves into and back out of. The
	// rows in it stay reachable — through a read surface of their own, or
	// through the same one — so entering it is not a removal and a zero
	// count on the transition says the row was already there, which is
	// the guarded claim the package doc separates out.
	ReversibleState LivenessRole = "reversible state"

	// SingleUseClaim is a column a row can only ever cross once, written
	// under a guard so that exactly one writer wins. A zero count answers
	// "did my claim win", not "does the row exist".
	SingleUseClaim LivenessRole = "single-use claim"

	// RowContent is a column that records something about the row rather
	// than whether the row is in service. The reads return it either way.
	RowContent LivenessRole = "row content"
)

// livenessGroup is one set of schema columns sharing a role and the reason
// they hold it.
//
// A column is named bare to classify it wherever it appears, or qualified
// as `table.column` to classify it on one table only. The bare form is
// what keeps the 66 enabled columns to a single entry; the qualified form
// exists because the same name is not the same decision everywhere — a
// column the reads exclude on one table and merely order by on another
// removes a row in the first case and not in the second, and a per-name
// vocabulary would have to answer for both at once and be wrong about one.
//
// A qualified entry only holds where it disagrees with the bare one. The
// check below rejects one that agrees, or that names a column the schema
// does not carry, so an override cannot outlive what it overrode.
type livenessGroup struct {
	Role    LivenessRole
	Reason  string
	Columns []string
}

// livenessVocabulary classifies every liveness-shaped column in the
// schema. The candidates are derived; this is the hand-written half, and
// the check below fails when the two stop agreeing.
var livenessVocabulary = []livenessGroup{
	{
		Role: RemovalMarker,
		Reason: "the reads restrict on it, so a row that has crossed it and a row that " +
			"never existed are the same answer to the caller, and nothing puts it back",
		Columns: []string{"enabled", "deleted_at", "revoked_at"},
	},
	{
		Role: DerivedMarker,
		Reason: "computed from a removal marker and so not assignable by any statement; it " +
			"carries that marker's liveness into a unique key, going NULL once the row is " +
			"removed so the key constrains only the live rows",
		Columns: []string{"active", "task_singleton_role"},
	},
	{
		Role: RemovalMarker,
		Reason: "archiving a notification is where the inbox ends, not a shelf beside it: " +
			"every read of the inbox and every unread count restricts on it, and no " +
			"statement clears it, so an archived notification is gone as far as any caller " +
			"can tell",
		Columns: []string{"notifications.archived_at"},
	},
	{
		Role: ReversibleState,
		Reason: "the row stays reachable in this state — a writer moves it back, or a read " +
			"surface still returns it — so entering the state is a claim on the row, not a " +
			"removal of it",
		Columns: []string{
			"archived_at", "is_archived", "is_active", "is_muted", "locked_until_at",
			"paused", "read_at", "snooze_until", "visible",
		},
	},
	{
		Role: SingleUseClaim,
		Reason: "the guard exists so two concurrent writers cannot both consume the row; a " +
			"zero count reports the loser, and the row it lost on is present",
		Columns: []string{"accepted_at", "applied_at", "claimed_at", "used_at"},
	},
	{
		Role: RowContent,
		Reason: "a setting or a capability the row carries, toggled in both directions and " +
			"never consulted to decide whether the row is there",
		Columns: []string{
			"all_day", "auto_action_enabled", "can_edit", "feature_calendar",
			"feature_lenses", "feature_pages", "feature_timeboxes", "is_ai_generated",
			"is_default", "is_public", "notif_email_assignment_enabled",
			"notif_email_digest_enabled", "notif_email_due_soon_enabled",
			"notif_email_mention_enabled", "notif_web_push_enabled", "supports_tools",
			"supports_vision", "treat_holidays_as_non_working",
		},
	},
	{
		Role: RowContent,
		Reason: "records that a step happened to the row, or when; the reads return the row " +
			"before and after it is set",
		Columns: []string{
			"added_at", "completed_at", "delivered_at", "done", "edited_at",
			"email_verified_at", "failed_at", "finished_at", "invited_at", "joined_at",
			"last_login_at", "last_refreshed_at", "last_used_at", "mfa_confirmed_at",
			"resolved_at", "safety_checked_at", "satisfied_at", "scored_at", "sent_at",
			"shared_at", "started_at", "updated_at", "uploaded_at",
		},
	},
	{
		Role: RowContent,
		Reason: "a point on the calendar or the end of a validity window; it is compared " +
			"against a time rather than against presence",
		Columns: []string{
			"access_token_expires_at", "due_on", "end_at", "ended_on", "expires_at",
			"next_retry_at", "recurrence_end", "recurrence_original_start", "start_at",
			"started_on",
		},
	},
}

// splitQualified separates a `table.column` entry. A bare entry reports an
// empty table.
func splitQualified(entry string) (table, column string) {
	if at := strings.IndexByte(entry, '.'); at >= 0 {
		return entry[:at], entry[at+1:]
	}
	return "", entry
}

// bareRoles classifies a column wherever it appears; qualifiedRoles
// classifies one table's column and wins over it.
var bareRoles, qualifiedRoles = buildRoles()

func buildRoles() (bare, qualified map[string]LivenessRole) {
	bare = map[string]LivenessRole{}
	qualified = map[string]LivenessRole{}
	for _, group := range livenessVocabulary {
		for _, entry := range group.Columns {
			if table, _ := splitQualified(entry); table != "" {
				qualified[entry] = group.Role
				continue
			}
			bare[entry] = group.Role
		}
	}
	return bare, qualified
}

// ClearingTables returns the tables on which a column is written back to
// NULL, out of a revival scan.
func ClearingTables(revived map[string][]Statement, column string) map[string]bool {
	out := map[string]bool{}
	for key := range revived {
		if table, name := splitQualified(key); name == column {
			out[table] = true
		}
	}
	return out
}

// RoleFor returns what a column does to the live set on one table, and
// whether the vocabulary accounts for it at all.
func RoleFor(table, column string) (LivenessRole, bool) {
	if role, ok := qualifiedRoles[table+"."+column]; ok {
		return role, true
	}
	role, ok := bareRoles[column]
	return role, ok
}

// RemovalMarkerColumns returns every column name classified as a removal
// marker somewhere, which is the set the removal shape has to be matched
// in before RoleFor decides whether it applies to the table at hand.
func RemovalMarkerColumns() []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range livenessVocabulary {
		if group.Role != RemovalMarker {
			continue
		}
		for _, entry := range group.Columns {
			_, column := splitQualified(entry)
			if seen[column] {
				continue
			}
			seen[column] = true
			out = append(out, column)
		}
	}
	return out
}

// ClassifiedEntries returns every vocabulary entry as written, mapped to
// its role, so a check can tell a bare entry from a qualified one.
func ClassifiedEntries() map[string]LivenessRole {
	out := map[string]LivenessRole{}
	for _, group := range livenessVocabulary {
		for _, entry := range group.Columns {
			out[entry] = group.Role
		}
	}
	return out
}

// clearedColumn matches an assignment that puts a column back to NULL.
var clearedColumn = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\s*=\s*null\b`)

// RevivedColumns returns, for each `table.column` some statement writes
// back to NULL, the statements that do it.
//
// Writing a tombstone column back to NULL puts the row into the set the
// reads return, which is what separates a state the row comes out of from
// a removal. It is keyed by table because that is where the two part
// company: the same column name is cleared on one table and never on
// another, and only the first of those is a state the row leaves.
//
// A flag marker is not reached by this: it is NOT NULL, so no statement
// can clear it. Re-enabling one is a different act — the schema keeps
// liveness out of its unique keys, so a revoked row holds on to its tuple
// and a caller granting the same pair again has to reuse that row. The
// reads never returned it in between.
func RevivedColumns(statements []Statement) map[string][]Statement {
	out := map[string][]Statement{}
	for _, s := range statements {
		if head(s.SQL) != "update" {
			continue
		}
		table := updateTarget(s.SQL)
		set, _ := updateClauses(s.SQL)
		for _, found := range clearedColumn.FindAllStringSubmatch(set, -1) {
			key := table + "." + found[1]
			out[key] = append(out[key], s)
		}
	}
	return out
}

// LivenessCandidate is one table's column, shaped so that it could carry
// liveness.
//
// Every table declaring the column is a candidate of its own, because the
// classification is answered per table: a name settled on one table says
// nothing about what the reads of another do with it.
type LivenessCandidate struct {
	Column string
	Table  string
	// Generated records that the column is computed from a removal
	// marker, which is what fixes its role without anyone listing it.
	Generated bool
}

// Qualified renders the candidate the way the vocabulary names it.
func (c LivenessCandidate) Qualified() string {
	return c.Table + "." + c.Column
}

// ReadSchema returns the contents of sql/schema.sql under root. The file
// is generated from sql/core and sql/flow, so it is the one place where
// every table's columns are visible at once.
func ReadSchema(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "sql", "schema.sql")) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// LivenessCandidates returns the columns of a schema that carry the shape
// a liveness marker needs, ordered by name.
//
// Two shapes qualify, and neither depends on what the column is called:
//
//	flag        a boolean the row always holds, so one of its two values
//	            can mean the row is no longer in service.
//	tombstone   a nullable temporal column, so its absence is the live
//	            state and writing it is the transition out.
//
// A generated column joins them when its expression names a removal
// marker: it is the schema's other spelling of the same liveness, carried
// into an index.
//
// The shapes over-collect on purpose. Separating a tombstone from an
// ordinary lifecycle timestamp needs to know what the reads do with it,
// which the column definition does not say — so the schema decides what
// has to be answered for and the vocabulary answers it.
func LivenessCandidates(schema string) []LivenessCandidate {
	markers := map[string]bool{}
	for _, column := range RemovalMarkerColumns() {
		markers[column] = true
	}

	var out []LivenessCandidate
	for _, table := range parseCreateTables(schema) {
		for _, column := range table.columns {
			switch {
			case column.generated:
				if mentionsAnyColumn(column.definition, markers) {
					out = append(out, LivenessCandidate{Column: column.name, Table: table.name, Generated: true})
				}
			case isFlagShape(column.definition), isTombstoneShape(column.definition):
				out = append(out, LivenessCandidate{Column: column.name, Table: table.name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Qualified() < out[j].Qualified() })
	return out
}

// isFlagShape reports whether a column definition is a boolean every row
// carries. An unsigned TINYINT is excluded: it is how this schema spells a
// small counter and a generated index key.
func isFlagShape(definition string) bool {
	upper := strings.ToUpper(definition)
	switch {
	case strings.HasPrefix(upper, "BOOLEAN"), strings.HasPrefix(upper, "BOOL "):
		return true
	case strings.HasPrefix(upper, "TINYINT(1)"):
		return true
	case strings.HasPrefix(upper, "TINYINT"):
		return !strings.Contains(upper, "UNSIGNED")
	default:
		return false
	}
}

// isTombstoneShape reports whether a column definition is a temporal
// column that may be absent, which is what lets its absence stand for the
// live state.
func isTombstoneShape(definition string) bool {
	upper := strings.ToUpper(definition)
	temporal := strings.HasPrefix(upper, "DATETIME") ||
		strings.HasPrefix(upper, "TIMESTAMP") ||
		strings.HasPrefix(upper, "DATE ") ||
		upper == "DATE"
	return temporal && !strings.Contains(upper, "NOT NULL")
}

// mentionsAnyColumn reports whether an expression names one of the given
// columns as a whole identifier.
func mentionsAnyColumn(expr string, columns map[string]bool) bool {
	lower := strings.ToLower(expr)
	for column := range columns {
		if mentionsColumn(lower, column) {
			return true
		}
	}
	return false
}

func mentionsColumn(lower, column string) bool {
	for offset := 0; ; {
		at := strings.Index(lower[offset:], column)
		if at < 0 {
			return false
		}
		start := offset + at
		end := start + len(column)
		before := start == 0 || !isIdentifierByte(lower[start-1])
		after := end >= len(lower) || !isIdentifierByte(lower[end])
		if before && after {
			return true
		}
		offset = end
	}
}

func isIdentifierByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ---------------------------------------------------------------------
// Schema parsing
// ---------------------------------------------------------------------

type schemaTable struct {
	name    string
	columns []schemaColumn
}

type schemaColumn struct {
	name string
	// definition is everything after the name, with its COMMENT text
	// removed so the type keywords can be read without the prose.
	definition string
	generated  bool
}

// indexDefinitions are the table items that declare no column.
var indexDefinitions = []string{
	"PRIMARY KEY", "UNIQUE KEY", "UNIQUE INDEX", "KEY ", "INDEX ",
	"FULLTEXT", "SPATIAL", "CONSTRAINT", "CHECK ", "FOREIGN KEY",
}

// parseCreateTables splits every CREATE TABLE body into its columns.
// Views are not reached: they declare no columns of their own, and their
// liveness comes from the tables underneath.
//
// The scan tracks string literals and parentheses because column comments
// in this schema hold both parentheses and the `--` sequence, which a
// line-oriented split reads as structure.
func parseCreateTables(text string) []schemaTable {
	const marker = "CREATE TABLE "
	var tables []schemaTable
	for offset := 0; ; {
		idx := strings.Index(text[offset:], marker)
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
		table := schemaTable{name: name}
		for _, item := range splitTopLevel(text[bodyStart:bodyEnd]) {
			if column, ok := parseColumn(item); ok {
				table.columns = append(table.columns, column)
			}
		}
		tables = append(tables, table)
		offset = bodyEnd
	}
}

// parseColumn turns one table item into a column, or reports that the item
// declares an index rather than a column.
func parseColumn(item string) (schemaColumn, bool) {
	upper := strings.ToUpper(item)
	for _, prefix := range indexDefinitions {
		if strings.HasPrefix(upper, prefix) {
			return schemaColumn{}, false
		}
	}
	fields := strings.Fields(item)
	if len(fields) < 2 {
		return schemaColumn{}, false
	}
	definition := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item), fields[0]))
	return schemaColumn{
		name:       strings.Trim(fields[0], "`"),
		definition: definition,
		generated:  strings.Contains(upper, "GENERATED ALWAYS AS"),
	}, true
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

// splitTopLevel splits a table body on the commas separating its
// definitions, dropping comments and the text of string literals and
// ignoring commas nested inside parentheses.
func splitTopLevel(body string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(body); {
		switch {
		case body[i] == '\'':
			i = skipString(body, i)
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
