// Package affectedrows derives, from the committed SQL, the statements
// whose affected-row count answers "was the row there" — and is therefore
// the only thing standing between a caller naming something that does not
// exist and a 2xx that says it was changed.
//
// Not every mutating statement can answer that. The connection does not
// set CLIENT_FOUND_ROWS, so MySQL reports changed rows rather than matched
// rows, and for most shapes a zero count is ambiguous:
//
//	INSERT      a row is written or the statement fails; the count adds
//	            nothing the error does not already carry.
//	plain       UPDATE ... SET a = ? WHERE public_id = ? counts zero when
//	UPDATE      the row already held that value. Re-submitting a patch is
//	            indistinguishable from a missing row.
//	guarded     UPDATE ... SET archived_at = NOW() WHERE archived_at IS
//	claim       NULL counts zero when the row exists and is already in the
//	            target state. That is what makes the query an atomic claim:
//	            it answers "did my claim win", not "does the row exist".
//
// One shape is unambiguous, and it is the shape this package derives:
//
//	DELETE            zero rows means nothing matched the predicate.
//	soft delete       UPDATE ... SET enabled = FALSE ... WHERE ... enabled
//	                  counts zero when no live row matched. The reads that
//	                  answer a caller asking for a resource filter on
//	                  enabled = TRUE, so "already disabled" and "never
//	                  existed" are the same answer to that caller: the
//	                  resource is not there.
//
// The premise is scoped to those reads, and deliberately so. Other reads
// see past the marker because seeing past it is their job — detecting the
// replay of a credential that was revoked, showing an operator what was
// removed, sweeping tombstoned rows before their storage goes. None of
// them answers a request for the resource, so none of them makes a
// disabled row visible to the caller a zero count concerns.
//
// For those statements a zero count is exactly the 404 the caller is owed,
// so dropping it turns a request that changed nothing into a success —
// together with the audit entry and the timeline event that say it did.
//
// The soft-delete shape is one shape written in several columns: the
// enabled flag, and the tombstone timestamps whose absence the reads take
// as the live state. Which columns those are is the vocabulary in
// liveness.go, and it is asserted against the schema rather than assumed —
// a convention nobody classified fails the check instead of quietly
// carrying statements out of it.
//
// The scope is derived rather than listed. A removal statement added
// tomorrow is checked without anyone remembering it exists, and a statement
// that leaves the removal shape leaves the scope with it.
package affectedrows

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MarkerForm is the machine-readable exemption, written as a comment above
// the statement in sql/queries or above the call in Go.
//
// The reason is mandatory and has to read as prose: a marker that states no
// reason is what the free-text exemptions elsewhere in this repository
// decayed into, where the value was written once and never read again.
const MarkerForm = "affected-rows: not-applicable — <why the count cannot answer>"

// MarkerPattern matches MarkerForm. Requiring the reason to start and end
// with a letter is what stops a mention of the marker from acting as one,
// which is the rule the direct-SQL gate in internal/mcp uses for its own
// exemption.
var MarkerPattern = regexp.MustCompile("affected-rows:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]")

// RemovalKind is how a statement takes a row out of the live set, or the
// empty string when it does not.
type RemovalKind string

const (
	// NotRemoval marks a statement whose zero count reports "nothing
	// changed" rather than "nothing matched".
	NotRemoval RemovalKind = ""
	// HardDelete is a DELETE.
	HardDelete RemovalKind = "DELETE"
	// SoftDelete is an UPDATE that writes a removal marker the reads
	// filter on, guarded on that same marker.
	SoftDelete RemovalKind = "soft delete"
)

// Statement is one named statement in sql/queries.
type Statement struct {
	// Name is the sqlc query name.
	Name string
	// Annotation is the sqlc annotation without its colon: exec,
	// execrows, execlastid, execresult, one, many.
	Annotation string
	// Path is the repository-relative file the statement lives in and
	// Line the 1-based line of its header, so failures point at it.
	Path string
	Line int
	// Comment is the comment block between the header and the first line
	// of SQL. Only that block can carry a marker: a comment trailing the
	// statement belongs to whatever comes next.
	Comment string
	// SQL is the statement with its comments stripped, lowercased and
	// its whitespace collapsed.
	SQL string
}

// Location renders the statement's position for a failure message.
func (s Statement) Location() string {
	return fmt.Sprintf("%s:%d", s.Path, s.Line)
}

// Marked reports whether the statement's comment block carries a marker.
func (s Statement) Marked() bool {
	return MarkerPattern.MatchString(s.Comment)
}

// RemovalKind reports whether a zero affected-row count on this statement
// means no row matched, which is the caller's 404, rather than a row that
// already held the values it was asked to hold.
func (s Statement) RemovalKind() RemovalKind {
	switch head(s.SQL) {
	case "delete":
		return HardDelete
	case "update":
		table := updateTarget(s.SQL)
		set, where := updateClauses(s.SQL)
		for _, marker := range removalMarkers {
			// The same column removes a row on one table and merely
			// shelves it on another, so the table the statement writes
			// decides which it is doing here.
			if role, _ := RoleFor(table, marker.column); role != RemovalMarker {
				continue
			}
			if marker.written(set) && marker.guarded(where) {
				return SoftDelete
			}
		}
		return NotRemoval
	default:
		return NotRemoval
	}
}

// NamedByTheCaller reports whether the statement's predicate is keyed on
// the public id the caller supplied.
//
// That is what turns a zero count into a 404 rather than into ordinary
// housekeeping: the caller asked for one resource by its identifier and
// nothing matched it. A removal keyed on an internal id, a token, or a
// whole workspace answers a question nobody asked in those terms — the
// blob collectors and the cascade sweeps are keyed that way — and there is
// no not-found response for them to produce.
func (s Statement) NamedByTheCaller() bool {
	return strings.Contains(predicate(s.SQL), "public_id")
}

// CountIsReachable reports whether the caller of this statement can see the
// affected-row count at all. sqlc hands back only an error for :exec, so a
// removal written that way has thrown the answer away before any caller
// gets a chance to read it.
func (s Statement) CountIsReachable() bool {
	return s.Annotation == "execrows" || s.Annotation == "execresult"
}

// GuardedClaim reports whether this statement's predicate restricts on
// something beyond the rows the caller named, so that a zero count says
// the claim did not win.
//
// The package doc above names the shape and declines to read a 404 out of
// it, which stands: "already in the target state" and "never there" are
// the same zero, so nothing here can be turned into a not-found. What the
// zero does say, unambiguously, is that this statement changed nothing —
// and that is worth reading whenever the caller goes on to say it changed
// something. Every removal is also a guarded claim; the two are separate
// questions about the same count, and a statement is asked both.
//
// The guard is read off the predicate rather than off a list of columns. A
// term comparing a column to a value the caller supplied is that caller
// naming rows; anything else — a bare boolean column, a comparison against
// a literal, an IS NULL — is a condition on the row's state that the rows
// can stop meeting between the read that chose them and the write.
func (s Statement) GuardedClaim() bool {
	switch head(s.SQL) {
	case "update", "delete":
		return guardedPredicate(predicate(s.SQL))
	default:
		return false
	}
}

// guardedPredicate reports whether a normalized WHERE clause constrains a
// column against anything other than a value its caller supplied.
//
// It works by subtraction: the caller-supplied comparisons are removed,
// then the connectives and constants that carry no column, and whatever
// column name is still standing is the state the write is conditional on.
// Subtracting rather than enumerating is what keeps a predicate spelled in
// an unanticipated way inside the shape rather than quietly outside it.
func guardedPredicate(where string) bool {
	rest := boundComparison.ReplaceAllString(where, " ")
	rest = predicateNoise.ReplaceAllString(rest, " ")
	return predicateIdentifier.MatchString(rest)
}

var (
	// boundComparison matches a column tested against a value the caller
	// passed in, in either spelling sqlc emits.
	boundComparison = regexp.MustCompile(
		`[a-z_][a-z0-9_.]*\s*(?:=|<>|!=|<=|>=|<|>)\s*(?:\?|sqlc\.[a-z]+\([^)]*\))` +
			`|[a-z_][a-z0-9_.]*\s+in\s*\(\s*(?:\?|sqlc\.[a-z]+\([^)]*\))[^)]*\)`)
	// predicateNoise matches what a predicate holds besides column names:
	// its connectives, its literals, and the placeholders left over from a
	// comparison whose column has already been subtracted.
	predicateNoise = regexp.MustCompile(
		`\b(?:and|or|not|is|null|true|false|in|between|like|exists|limit|order|by|group|asc|desc)\b` +
			`|\?|\bsqlc\.[a-z]+\([^)]*\)|\b\d+\b|[^a-z0-9_]`)
	// predicateIdentifier matches a surviving column name.
	predicateIdentifier = regexp.MustCompile(`[a-z_][a-z0-9_]*`)
)

var headerPattern = regexp.MustCompile(`^--\s*name:\s*(\S+)\s+:(\S+)`)

// marker matches one removal-marker column inside a normalized clause.
type marker struct {
	column     string
	assignment *regexp.Regexp
	mention    *regexp.Regexp
}

// removalMarkers is the vocabulary the removal shape is read in: every
// column classified as a removal marker on any table. Whether it is one on
// the table a given statement writes is settled per statement, because the
// classification is keyed by table and column together.
var removalMarkers = compileMarkers(RemovalMarkerColumns())

func compileMarkers(columns []string) []marker {
	out := make([]marker, 0, len(columns))
	for _, column := range columns {
		out = append(out, marker{
			column:     column,
			assignment: regexp.MustCompile(`\b` + column + `\s*=\s*([^\s,]+)`),
			mention:    regexp.MustCompile(`\b` + column + `\b`),
		})
	}
	return out
}

// isLiveValue reports whether an assigned value leaves the row in the read
// set: the marker's live value, or a parameter, which says nothing about
// which direction the statement moves the row in.
func isLiveValue(value string) bool {
	return value == "true" || value == "null" || value == "?" ||
		strings.HasPrefix(value, "sqlc.")
}

// written reports whether a SET clause moves the marker to its removed
// value. `SET enabled = TRUE` and `SET deleted_at = NULL` are the same
// statement running backwards, and neither is a removal.
func (m marker) written(set string) bool {
	found := m.assignment.FindStringSubmatch(set)
	return found != nil && !isLiveValue(found[1])
}

// guarded reports whether a predicate restricts on the marker, which is
// what makes a zero count mean "no live row matched" rather than "the row
// already held this value".
func (m marker) guarded(where string) bool {
	return m.mention.MatchString(where)
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
			return "", errors.New("affectedrows: go.work not found above the working directory")
		}
		dir = parent
	}
}

// Statements reads every named statement under sql/queries, in file order.
func Statements(root string) ([]Statement, error) {
	queriesDir := filepath.Join(root, "sql", "queries")
	var out []Statement
	err := filepath.WalkDir(queriesDir, func(path string, entry fs.DirEntry, walkErr error) error {
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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, parseFile(filepath.ToSlash(rel), string(raw))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Removals narrows a statement list down to the removal statements.
func Removals(statements []Statement) []Statement {
	var out []Statement
	for _, s := range statements {
		if s.RemovalKind() != NotRemoval {
			out = append(out, s)
		}
	}
	return out
}

// parseFile cuts a query file at its sqlc `-- name:` headers.
func parseFile(path, text string) []Statement {
	var out []Statement
	var current *Statement
	var comment, body []string
	inComment := true

	flush := func() {
		if current == nil {
			return
		}
		current.Comment = strings.Join(comment, "\n")
		current.SQL = normalize(strings.Join(body, "\n"))
		out = append(out, *current)
		current = nil
		comment = nil
		body = nil
	}

	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if match := headerPattern.FindStringSubmatch(trimmed); match != nil {
			flush()
			current = &Statement{
				Name:       match[1],
				Annotation: match[2],
				Path:       path,
				Line:       i + 1,
			}
			inComment = true
			continue
		}
		if current == nil {
			continue
		}
		body = append(body, line)
		if !inComment {
			continue
		}
		switch {
		case trimmed == "":
			comment = append(comment, line)
		case strings.HasPrefix(trimmed, "--"):
			comment = append(comment, line)
		default:
			inComment = false
		}
	}
	flush()
	return out
}

// normalize strips comments, lowercases and collapses whitespace so a
// clause can be matched without caring how it was wrapped.
func normalize(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if code, _, found := strings.Cut(line, "--"); found {
			line = code
		}
		out.WriteString(line)
		out.WriteString(" ")
	}
	return strings.Join(strings.Fields(strings.ToLower(out.String())), " ")
}

// head returns the leading keyword of a normalized statement.
func head(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// updateTarget returns the table a normalized UPDATE writes. The name may
// be followed by an alias, which is not part of it.
func updateTarget(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) < 2 || fields[0] != "update" {
		return ""
	}
	return fields[1]
}

// updateClauses splits a normalized UPDATE into its assignments and its
// predicate. Reading the whole statement instead would call `SET enabled =
// FALSE WHERE enabled = TRUE` and `SET enabled = TRUE WHERE enabled =
// FALSE` the same thing, and only the first of those is a removal.
func updateClauses(sql string) (set, where string) {
	at := setKeyword.FindStringIndex(sql)
	if at == nil {
		return "", ""
	}
	rest := sql[at[1]:]
	if end := whereKeyword.FindStringIndex(rest); end != nil {
		return rest[:end[0]], rest[end[1]:]
	}
	return rest, ""
}

// predicate returns what a normalized statement restricts on.
func predicate(sql string) string {
	at := whereKeyword.FindStringIndex(sql)
	if at == nil {
		return ""
	}
	return sql[at[1]:]
}

var (
	setKeyword   = regexp.MustCompile(`\bset\b`)
	whereKeyword = regexp.MustCompile(`\bwhere\b`)
)
