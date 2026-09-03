package affectedrows

import (
	"strings"
	"testing"
)

// TestRemovalStatementsKeepTheirAffectedRowCount refuses a removal
// statement written as :exec without saying why.
//
// sqlc hands a :exec query back as a bare error, so the count is gone
// before any caller can look at it: the handler cannot tell a delete that
// matched a row from one that matched nothing, and answers ok either way.
// For the removal shape that count is the whole answer, so writing one as
// :exec is a decision, and this makes the decision state its reason.
//
// Nothing is listed here. The statements come out of sql/queries and the
// shape out of the SQL itself, so a removal added tomorrow is checked
// without anyone registering it.
func TestRemovalStatementsKeepTheirAffectedRowCount(t *testing.T) {
	t.Parallel()

	statements := removalStatements(t)
	for _, s := range statements {
		if s.CountIsReachable() || s.Marked() {
			continue
		}
		t.Errorf("%s: %s is a %s written as :exec, so its affected-row count never "+
			"reaches the caller and a request that matched nothing is answered the same "+
			"as one that removed a row. Return the count with :execrows and map zero onto "+
			"the not-found error for the resource, or say above the statement why the "+
			"count cannot answer here: %s",
			s.Location(), s.Name, s.RemovalKind(), MarkerForm)
	}
}

// TestAffectedRowMarkersStillApply drops a marker that has outlived what
// it exempted.
//
// A marker is a claim that this statement's count cannot answer the
// caller's question. Once the statement returns its count, or stops being
// a removal, the claim is no longer about anything — and a reader who
// finds it there concludes the statement was considered and cleared.
func TestAffectedRowMarkersStillApply(t *testing.T) {
	t.Parallel()

	for _, s := range allStatements(t) {
		if !s.Marked() {
			continue
		}
		switch {
		case s.RemovalKind() == NotRemoval:
			t.Errorf("%s: %s carries an affected-rows marker but is not a removal, so "+
				"nothing was checking it. A zero count on this shape means the row already "+
				"held these values, which no gate asks about; drop the marker",
				s.Location(), s.Name)
		case s.CountIsReachable():
			t.Errorf("%s: %s carries an affected-rows marker but returns its count as "+
				":%s, so the caller can answer with it after all. Drop the marker and "+
				"check the count at the call site",
				s.Location(), s.Name, s.Annotation)
		}
	}
}

// TestRemovalDerivationStillMatches is the positive control for the SQL
// side: it proves the parser and the shape rule report what they are meant
// to report, rather than the whole check passing because the derivation
// quietly stopped matching.
func TestRemovalDerivationStillMatches(t *testing.T) {
	t.Parallel()

	const src = `-- name: DeleteRow :execrows
-- Removes the row outright.
DELETE FROM t
WHERE public_id = ?;

-- name: DisableRow :exec
-- affected-rows: not-applicable — the row is resolved before this runs.
UPDATE t
SET enabled = FALSE
WHERE public_id = ?
  AND enabled = TRUE;

-- name: ReviveRow :execrows
UPDATE t
SET enabled = TRUE
WHERE public_id = ?
  AND enabled = FALSE;

-- name: RenameRow :exec
UPDATE t
SET name = ?
WHERE public_id = ?;

-- name: ArchiveRow :exec
UPDATE t
SET archived_at = NOW()
WHERE public_id = ?
  AND archived_at IS NULL;

-- name: AddRow :execlastid
INSERT INTO t (public_id) VALUES (?);
`

	got := parseFile("sample.sql", src)
	want := []struct {
		name       string
		annotation string
		line       int
		kind       RemovalKind
		marked     bool
	}{
		{"DeleteRow", "execrows", 1, HardDelete, false},
		{"DisableRow", "exec", 6, SoftDelete, true},
		{"ReviveRow", "execrows", 13, NotRemoval, false},
		{"RenameRow", "exec", 19, NotRemoval, false},
		{"ArchiveRow", "exec", 24, NotRemoval, false},
		{"AddRow", "execlastid", 30, NotRemoval, false},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d statements, want %d", len(got), len(want))
	}
	for i, w := range want {
		s := got[i]
		if s.Name != w.name || s.Annotation != w.annotation || s.Line != w.line {
			t.Errorf("statement %d is %s :%s at line %d, want %s :%s at line %d",
				i, s.Name, s.Annotation, s.Line, w.name, w.annotation, w.line)
		}
		if kind := s.RemovalKind(); kind != w.kind {
			t.Errorf("%s is classified %q, want %q", w.name, kind, w.kind)
		}
		if s.Marked() != w.marked {
			t.Errorf("%s marked=%v, want %v", w.name, s.Marked(), w.marked)
		}
	}
}

// TestAffectedRowMarkerNeedsAReason pins the rule that makes the marker
// worth reading: the exemption is the reason, so the token on its own is
// not one.
func TestAffectedRowMarkerNeedsAReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		comment string
		want    bool
	}{
		{"reason", "-- affected-rows: not-applicable — a sweep runs against whatever expired.", true},
		{"bare", "-- affected-rows: not-applicable", false},
		{"empty reason", "-- affected-rows: not-applicable — ", false},
		{"hyphen instead of the dash", "-- affected-rows: not-applicable - a sweep.", false},
		{"mention", "-- see the affected-rows rule in tests/affectedrows", false},
	} {
		if got := MarkerPattern.MatchString(tc.comment); got != tc.want {
			t.Errorf("%s: matched=%v, want %v for %q", tc.name, got, tc.want, tc.comment)
		}
	}
}

// removalStatements returns the removal statements in sql/queries, and
// fails when there are none: this check reads files by path, and a
// derivation that has stopped matching passes for the wrong reason.
func removalStatements(t *testing.T) []Statement {
	t.Helper()
	statements := Removals(allStatements(t))
	if len(statements) == 0 {
		t.Fatal("no statement under sql/queries carries the removal shape; the SQL parser " +
			"has stopped matching rather than the removals having gone away")
	}
	return statements
}

func allStatements(t *testing.T) []Statement {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	statements, err := Statements(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	if len(statements) == 0 {
		t.Fatal("no statements found under sql/queries")
	}
	for _, s := range statements {
		if !strings.HasPrefix(s.Path, "sql/queries/") {
			t.Fatalf("statement %s was read from %s, outside sql/queries", s.Name, s.Path)
		}
	}
	return statements
}
