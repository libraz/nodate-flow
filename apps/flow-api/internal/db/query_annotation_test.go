package db

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// queryNamePattern matches a sqlc query header: `-- name: Foo :exec`.
var queryNamePattern = regexp.MustCompile(`^--\s*name:\s*(\S+)\s+(:\w+)\s*$`)

// commentLine matches a leading SQL comment inside a query body, which
// has to be skipped before the statement's first keyword is readable.
var commentLine = regexp.MustCompile(`(?m)\A(--[^\n]*\n)+`)

// exemptions lists by-public_id mutations that may stay `:exec`, with the
// reason. It is empty, and that is the intended state: `:execrows` costs
// a caller nothing it can't ignore, so there has been no case for an
// exception. An entry here is a claim that the affected-row count is
// genuinely unusable at every call site, and it should be argued in the
// value.
var exemptions = map[string]string{}

// TestMutationsByPublicIDReturnAffectedRows requires every UPDATE or
// DELETE that identifies its target by public_id to be declared
// `:execrows`.
//
// public_id is the shape that makes the rule decidable. A statement
// keyed on it is answering about one specific row the caller named, so
// "nothing matched" is information the caller needs — it is the
// difference between "deleted" and "there was nothing there". Statements
// keyed on internal ids or on a filter are a different thing: a worker
// sweeping expired rows legitimately affects none, and this check leaves
// them alone.
//
// The defect being guarded is not one endpoint. More than ten of them
// independently discarded the count and answered ok — a token revoke
// reported success while the token stayed valid, an admin suspend
// reported success on a user id that did not exist, and each wrote the
// audit entry saying otherwise. A review-time rule had already failed to
// hold across that many sites, so the requirement is mechanical.
//
// Read the guarantee narrowly. This fixes only that the count reaches
// the caller; it cannot see whether the caller looks at it, and
// `if _, err := q.Delete(...)` passes here. Two things cover that gap,
// and neither is this test: the call sites that discard the count carry
// a written reason, and the behaviour is pinned end to end by
// tests/e2e/mutation_affected_rows_test.go, which drives each endpoint
// against an id that names nothing and requires a 404. That end-to-end
// test is the real guarantee; this one only keeps the option open.
//
// A text scan is also the limit of what a .sql file supports. There is
// no AST here to ask, so a header inside a block comment would be read
// as a real one. That is a cost worth paying versus not checking: the
// files are generated-from and machine-read by sqlc, which imposes the
// same one-header-per-query shape this relies on.
func TestMutationsByPublicIDReturnAffectedRows(t *testing.T) {
	t.Parallel()

	root := queriesRoot(t)
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".sql") {
			return nil
		}
		// The walk root is this repository's own sql/queries tree,
		// supplied by the test.
		b, readErr := os.ReadFile(path) //#nosec G304,G122 -- walk root is the repo query tree, fixed by the test
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		for _, q := range splitQueries(string(b)) {
			if q.annotation != ":exec" {
				continue
			}
			if !mutatesByPublicID(q.body) {
				continue
			}
			if reason, ok := exemptions[q.name]; ok && reason != "" {
				continue
			}
			offenders = append(offenders,
				fmt.Sprintf("%s:%d %s", rel, q.line, q.name))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk sql/queries: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("an UPDATE or DELETE keyed on public_id must be :execrows so the caller "+
			"can tell \"changed nothing\" from \"changed the row\"; these are still :exec:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestExemptionsCarryAReason keeps the escape hatch honest: an entry
// with an empty reason is not an exemption, it is a silent opt-out.
func TestExemptionsCarryAReason(t *testing.T) {
	t.Parallel()

	for name, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemption %q has no reason; state why the affected-row count is "+
				"unusable at every call site, or drop the entry", name)
		}
	}
}

type sqlcQuery struct {
	name       string
	annotation string
	line       int
	body       string
}

// splitQueries parses a sqlc query file into its declared queries.
func splitQueries(src string) []sqlcQuery {
	lines := strings.Split(src, "\n")
	type start struct {
		idx        int
		name       string
		annotation string
	}
	var starts []start
	for i, l := range lines {
		if m := queryNamePattern.FindStringSubmatch(l); m != nil {
			starts = append(starts, start{idx: i, name: m[1], annotation: m[2]})
		}
	}
	out := make([]sqlcQuery, 0, len(starts))
	for n, s := range starts {
		end := len(lines)
		if n+1 < len(starts) {
			end = starts[n+1].idx
		}
		out = append(out, sqlcQuery{
			name:       s.name,
			annotation: s.annotation,
			line:       s.idx + 1,
			body:       strings.Join(lines[s.idx+1:end], "\n"),
		})
	}
	return out
}

// mutatesByPublicID reports whether body is an UPDATE or DELETE whose
// WHERE clause names public_id.
//
// The WHERE clause is what decides it, not the statement as a whole: an
// UPDATE that *sets* a column from a public id but selects its rows some
// other way is not answering about one named row.
func mutatesByPublicID(body string) bool {
	stmt := strings.TrimSpace(commentLine.ReplaceAllString(strings.TrimSpace(body), ""))
	upper := strings.ToUpper(stmt)
	if !strings.HasPrefix(upper, "UPDATE") && !strings.HasPrefix(upper, "DELETE") {
		return false
	}
	idx := strings.Index(upper, "WHERE")
	if idx < 0 {
		return false
	}
	return strings.Contains(upper[idx:], "PUBLIC_ID")
}

// queriesRoot returns the repository's sql/queries directory. Tests run
// in the package directory, so it is four levels up from
// apps/flow-api/internal/db.
func queriesRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "sql", "queries"))
	if err != nil {
		t.Fatalf("resolve sql/queries: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("expected the query tree at %s: %v", root, err)
	}
	return root
}
