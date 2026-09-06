package taskvisibility

import (
	"strings"
	"testing"
)

// controlSchema is a schema in the shape of the real one, small enough to
// reason about: the tasks table, a view that inherits its content and
// carries the rule's inputs, a view that inherits through the first one,
// and a view that exposes a title while dropping the inputs.
const controlSchema = `
CREATE TABLE tasks (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  project_id INT UNSIGNED NOT NULL,
  created_by_user_id INT UNSIGNED NULL,
  title VARCHAR(255) NOT NULL,
  visibility ENUM('public','project','private') NOT NULL
);

CREATE OR REPLACE ALGORITHM=MERGE VIEW v_ctl_all AS
SELECT
  t.project_id,
  t.created_by_user_id,
  -- A column comment with a semicolon in it; the parser has to survive one.
  t.id AS task_internal_id,
  t.title,
  t.visibility
FROM tasks t;

CREATE OR REPLACE VIEW v_ctl AS
SELECT v.*
FROM v_ctl_all v;

CREATE OR REPLACE VIEW v_ctl_headline AS
SELECT
  t.title AS headline
FROM tasks t;
`

// controlUnit is the canonical unit written out against the tasks table,
// as a statement author would have to write it.
const controlUnit = `    OR (
      t.visibility = 'public'
      OR (t.visibility = 'project' AND EXISTS (
        SELECT 1 FROM project_members pm_vis
        WHERE pm_vis.project_id = t.project_id
          AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm_vis.enabled = TRUE
      ))
      OR (t.visibility = 'private' AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1 FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      ))
    )
`

// TestScanSeesEachFailureShape is the positive control.
//
// Every assertion in this package is of the form "nothing was found", and
// a scan that has stopped scanning satisfies all of them. So the shapes
// are fed in deliberately here: a projection with no rule, a projection
// behind an edited copy of the rule, a projection from a relation that
// cannot carry it, a marker covering nothing — and, so the check is not
// merely noisy, the two shapes that must stay silent.
func TestScanSeesEachFailureShape(t *testing.T) {
	t.Parallel()

	sources := Sources(controlSchema)
	for _, want := range []string{"tasks", "v_ctl_all", "v_ctl", "v_ctl_headline"} {
		if _, ok := sources[want]; !ok {
			t.Fatalf("the view walk did not find %s, so the control proves nothing", want)
		}
	}
	if !sources["v_ctl"].Carries {
		t.Fatal("v_ctl inherits the rule's inputs through v_ctl_all and should carry the rule")
	}
	if sources["v_ctl_headline"].Carries {
		t.Fatal("v_ctl_headline exposes a title and none of the rule's inputs; " +
			"reporting it as able to carry the rule would excuse every statement over it")
	}
	if sources["v_ctl"].Content["title"] != "title" {
		t.Fatalf("v_ctl should inherit title through `SELECT v.*`; content is %v", sources["v_ctl"].Content)
	}
	if sources["v_ctl_headline"].Content["headline"] != "title" {
		t.Fatalf("v_ctl_headline should expose tasks.title under its own name; content is %v",
			sources["v_ctl_headline"].Content)
	}

	cases := []struct {
		name string
		sql  string
		want []FindingKind
	}{
		{
			name: "no rule at all",
			sql: `-- name: Bare :many
SELECT t.title FROM tasks t WHERE t.project_id = ?;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "rule present and canonical",
			sql: `-- name: Guarded :many
SELECT t.title FROM tasks t
WHERE t.project_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
` + controlUnit + `  );`,
			want: nil,
		},
		{
			name: "canonical through an inheriting view",
			sql: `-- name: GuardedView :many
SELECT v.title FROM v_ctl v
WHERE (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  );`,
			want: nil,
		},
		{
			name: "a branch dropped from the rule",
			sql: `-- name: Edited :many
SELECT t.title FROM tasks t
WHERE (
    t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = t.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
    ))
  );`,
			want: []FindingKind{Divergent},
		},
		{
			name: "a marker cannot excuse an edited rule",
			sql: `-- name: EditedAndMarked :many
-- task-visibility: not-applicable — this reason is well formed and still must not count.
SELECT t.title FROM tasks t
WHERE (
    t.visibility = 'public'
    OR (t.visibility = 'private' AND t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED))
  );`,
			want: []FindingKind{Divergent},
		},
		{
			name: "content from a relation that cannot carry the rule",
			sql: `-- name: Headline :many
SELECT v.headline FROM v_ctl_headline v WHERE v.headline LIKE ?;`,
			want: []FindingKind{NoAnchor},
		},
		{
			name: "a marker with a reason excuses an unguarded projection",
			sql: `-- name: BareButMarked :many
-- task-visibility: not-applicable — the caller resolves the task first.
SELECT t.title FROM tasks t WHERE t.project_id = ?;`,
			want: nil,
		},
		{
			name: "a marker with no reason excuses nothing",
			sql: `-- name: BareAndReasonless :many
-- task-visibility: not-applicable —
SELECT t.title FROM tasks t WHERE t.project_id = ?;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "a mention of the marker is not a marker",
			sql: `-- name: BareAndDiscussed :many
-- Statements like this one would need a task-visibility: not-applicable marker.
SELECT t.title FROM tasks t WHERE t.project_id = ?;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "a marker on a statement projecting no content",
			sql: `-- name: Counting :one
-- task-visibility: not-applicable — counts disclose nothing about a task.
SELECT COUNT(*) FROM tasks t WHERE t.project_id = ?;`,
			want: []FindingKind{StaleMarker},
		},
		{
			name: "a marker on a statement that carries the rule anyway",
			sql: `-- name: GuardedAndMarked :many
-- task-visibility: not-applicable — a reason that has outlived the predicate below.
SELECT t.title FROM tasks t
WHERE t.project_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
` + controlUnit + `  );`,
			want: []FindingKind{StaleMarker},
		},
		{
			name: "joining tasks without projecting their content",
			sql: `-- name: Joined :many
SELECT c.body FROM comments c INNER JOIN tasks t ON t.id = c.task_id WHERE t.project_id = ?;`,
			want: nil,
		},
		{
			name: "content through a derived table",
			sql: `-- name: Derived :many
SELECT public_id, title FROM (
  SELECT t.public_id AS public_id, t.title AS title
  FROM tasks t
  WHERE t.project_id = ?
) AS d
ORDER BY title ASC;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "the rule written inside a derived table",
			sql: `-- name: DerivedGuarded :many
SELECT title FROM (
  SELECT t.title AS title
  FROM tasks t
  WHERE t.project_id = ?
    AND (
      CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
` + controlUnit + `    )
) AS d;`,
			want: nil,
		},
		{
			name: "one branch of a derived UNION left without the rule",
			sql: `-- name: DerivedUnion :many
SELECT title FROM (
  SELECT t.title AS title
  FROM tasks t
  WHERE t.project_id = ?
    AND (
      CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
` + controlUnit + `    )
  UNION
  SELECT t2.title AS title
  FROM tasks t2
  WHERE t2.project_id = ?
) AS d;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "a star over a derived table",
			sql: `-- name: DerivedStar :many
SELECT * FROM (SELECT t.title AS title FROM tasks t WHERE t.project_id = ?) AS d;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "content through a CTE",
			sql: `-- name: CTEProjection :many
WITH recent (title) AS (
  SELECT t.title FROM tasks t WHERE t.project_id = ?
)
SELECT title FROM recent;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "a derived table over a relation that carries no task content",
			sql: `-- name: DerivedComments :many
SELECT body FROM (
  SELECT c.body AS body FROM comments c INNER JOIN tasks t ON t.id = c.task_id
) AS d;`,
			want: nil,
		},
		{
			name: "a derived table projecting no content column",
			sql: `-- name: DerivedNoContent :many
SELECT public_id FROM (SELECT t.public_id AS public_id FROM tasks t) AS d;`,
			want: nil,
		},
		{
			name: "a select-list entry wrapping a statement of its own",
			sql: `-- name: CorrelatedTitle :many
SELECT (SELECT t.title FROM tasks t WHERE t.id = c.task_id) AS title
FROM comments c;`,
			want: []FindingKind{Unguarded},
		},
		{
			name: "an unqualified content column with two relations to belong to",
			sql: `-- name: AmbiguousTitle :many
SELECT title FROM tasks t INNER JOIN comments c ON c.task_id = t.id WHERE t.project_id = ?;`,
			want: []FindingKind{Opaque},
		},
		{
			name: "a marker answers a projection nobody could follow",
			sql: `-- name: AmbiguousTitleMarked :many
-- task-visibility: not-applicable — the caller resolved the task before this ran.
SELECT title FROM tasks t INNER JOIN comments c ON c.task_id = t.id WHERE t.project_id = ?;`,
			want: nil,
		},
		{
			name: "a column a derived table could not follow either",
			sql: `-- name: DerivedAmbiguous :many
SELECT title FROM (
  SELECT title FROM tasks t INNER JOIN comments c ON c.task_id = t.id
) AS d;`,
			want: []FindingKind{Opaque},
		},
		{
			name: "an expression that names only columns which say nothing about the task",
			sql: `-- name: DerivedIdentifier :many
SELECT public_id_str FROM (
  SELECT BIN_TO_UUID(t.public_id, 0) AS public_id_str FROM tasks t
) AS d;`,
			want: nil,
		},
		{
			name: "an expression that wraps a title",
			sql: `-- name: DerivedWrappedTitle :many
SELECT label FROM (
  SELECT COALESCE(t.title, '') AS label FROM tasks t
) AS d;`,
			want: []FindingKind{Unguarded},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			statements := Statements(map[string]string{"control.sql": tc.sql})
			if len(statements) != 1 {
				t.Fatalf("expected one statement, parsed %d", len(statements))
			}
			findings, _, _ := Check(statements, sources)
			got := make([]FindingKind, 0, len(findings))
			for _, f := range findings {
				got = append(got, f.Kind)
			}
			if !sameKinds(got, tc.want) {
				t.Fatalf("expected %v, got %v", names(tc.want), names(got))
			}
		})
	}
}

// TestDerivedProjectionsNameTheRelationInside pins where a projection
// through a derived table is attributed.
//
// The kind of finding is not enough here. A statement that projects a
// title out of `( ... UNION ... ) AS linked` has to be reported against
// the alias inside that reads `tasks`, because that is where the rule
// would be written: an exposure reported against `linked` would name a
// relation exposing none of the predicate's inputs, and the author would
// be told to add a rule to a place it cannot go.
//
// The shape is the one this was blind to — a UNION under a derived table
// whose outer select list is unqualified — so the test also stands as the
// witness that the blindness is gone: before, this statement reported no
// exposure at all.
func TestDerivedProjectionsNameTheRelationInside(t *testing.T) {
	t.Parallel()

	const sql = `-- name: LinkedThroughUnion :many
SELECT public_id_str, title FROM (
  SELECT BIN_TO_UUID(t.public_id, 0) AS public_id_str, t.title AS title
  FROM tasks t
  INNER JOIN comments c ON c.task_id = t.id
  WHERE t.project_id = ?
  UNION
  SELECT BIN_TO_UUID(t.public_id, 0) AS public_id_str, t.title AS title
  FROM tasks t
  WHERE t.id = ?
) AS linked
ORDER BY title ASC;`

	statements := Statements(map[string]string{"control.sql": sql})
	if len(statements) != 1 {
		t.Fatalf("expected one statement, parsed %d", len(statements))
	}
	exposures, opaque := Exposures(statements[0], Sources(controlSchema))
	if len(opaque) != 0 {
		t.Errorf("the statement reports %v as unfollowable; every column in it is a plain "+
			"projection of a column, through one wrapper", opaque)
	}
	if len(exposures) != 1 {
		t.Fatalf("expected the title to be attributed to exactly one alias, got %d: %v",
			len(exposures), exposures)
	}
	if got := Describe(exposures[0]); got != "t.title (from tasks)" {
		t.Errorf("the projection is attributed to %s; it has to name the alias inside the "+
			"derived table that reads tasks, since that is the only place the rule can be "+
			"written", got)
	}
}

// TestNormalizeCollapsesOnlyWhatTheRuleDoesNotHave pins the normaliser to
// the differences it is allowed to ignore.
//
// It is the piece everything else rests on: normalise too much and two
// genuinely different predicates compare equal, which is exactly the
// failure this gate exists to catch.
func TestNormalizeCollapsesOnlyWhatTheRuleDoesNotHave(t *testing.T) {
	t.Parallel()

	base := "t.visibility = 'private' AND EXISTS (SELECT 1 FROM task_actors ta_vis " +
		"WHERE ta_vis.task_id = t.id AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED))"

	same := map[string]string{
		"line wrapping":  strings.ReplaceAll(base, " ", "\n  "),
		"subquery alias": strings.ReplaceAll(base, "ta_vis", "ta_other"),
		"spacing inside parentheses": strings.ReplaceAll(
			strings.ReplaceAll(base, "(SELECT", "( SELECT"), "UNSIGNED))", "UNSIGNED ) )"),
		"a trailing comment": base + " -- and a note about it",
		"letter case":        strings.ToUpper(base),
	}
	want := Normalize(base)
	for name, variant := range same {
		if got := Normalize(variant); got != want {
			t.Errorf("%s should normalise to the same text\n want: %s\n got:  %s", name, want, got)
		}
	}

	differs := map[string]string{
		"a different column":  strings.Replace(base, "ta_vis.task_id", "ta_vis.event_id", 1),
		"a dropped condition": strings.Replace(base, " AND ta_vis.user_id", " -- AND ta_vis.user_id", 1),
		"a different branch":  strings.Replace(base, "'private'", "'project'", 1),
		"a different table":   strings.Replace(base, "task_actors", "project_members", 1),
	}
	for name, variant := range differs {
		if got := Normalize(variant); got == want {
			t.Errorf("%s must not normalise to the same text as the original: %s", name, got)
		}
	}
}

func sameKinds(got, want []FindingKind) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[FindingKind]int{}
	for _, k := range got {
		seen[k]++
	}
	for _, k := range want {
		seen[k]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func names(kinds []FindingKind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		switch k {
		case Unguarded:
			out = append(out, "Unguarded")
		case Divergent:
			out = append(out, "Divergent")
		case NoAnchor:
			out = append(out, "NoAnchor")
		case Unreadable:
			out = append(out, "Unreadable")
		case Opaque:
			out = append(out, "Opaque")
		case StaleMarker:
			out = append(out, "StaleMarker")
		}
	}
	return out
}
