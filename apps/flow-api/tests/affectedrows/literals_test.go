package affectedrows

import "testing"

// TestInlineWritesAreClassifiedByTheSameShapes is the positive control for
// the literal half of the statement universe.
//
// A write built as a Go string literal is not a different kind of write,
// so it may not be a different kind of answer: the same SET clause and the
// same guard have to classify the same way whether the statement was
// declared in sql/queries or spelled inline. The cases here are the shapes
// the tree actually contains, so a classifier that drifts on one of them
// is caught by the shape rather than by whichever call site happens to use
// it today.
func TestInlineWritesAreClassifiedByTheSameShapes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		sql  string
		kind RemovalKind
		// statement is the name a failure message would use, empty when
		// the write carries no removal shape at all.
		statement string
	}{
		{
			name:      "soft delete guarded on the flag the reads filter on",
			sql:       "UPDATE calendar_events SET enabled = FALSE WHERE id = ? AND enabled",
			kind:      SoftDelete,
			statement: "the inline soft delete of calendar_events",
		},
		{
			name: "guarded claim on a state the row comes back out of",
			// Archiving a task is reversible, so a zero count reports a row
			// that was already archived rather than one that was never
			// there. The guard on the marker makes it an atomic claim, not
			// a removal.
			sql:  "UPDATE tasks SET archived_at = NOW(), updated_at = NOW() WHERE id = ? AND archived_at IS NULL",
			kind: NotRemoval,
		},
		{
			name:      "hard delete",
			sql:       "DELETE FROM task_event_links WHERE public_id = ?",
			kind:      HardDelete,
			statement: "the inline DELETE from task_event_links",
		},
		{
			name: "plain update",
			sql:  "UPDATE tasks SET title = ? WHERE id = ?",
			kind: NotRemoval,
		},
		{
			name: "the same flag written backwards",
			// Re-enabling a row is the removal running in reverse, and the
			// SET clause is the only thing that says so.
			sql:  "UPDATE calendar_events SET enabled = TRUE WHERE id = ? AND NOT enabled",
			kind: NotRemoval,
		},
		{
			name: "the flag written without a guard on it",
			// Nothing restricts the write to a live row, so a zero count
			// means the row already held the value rather than that no live
			// row matched.
			sql:  "UPDATE tasks SET enabled = FALSE WHERE id = ?",
			kind: NotRemoval,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			statement, ok := InlineStatement(tc.sql, "sample.go", 1)
			if want := tc.kind != NotRemoval; ok != want {
				t.Fatalf("InlineStatement reported ok=%v, want %v for %q", ok, want, tc.sql)
			}
			if !ok {
				return
			}
			if got := statement.RemovalKind(); got != tc.kind {
				t.Errorf("classified as %q, want %q", got, tc.kind)
			}
			if statement.Name != tc.statement {
				t.Errorf("named %q, want %q", statement.Name, tc.statement)
			}
			// A Go exec call hands back sql.Result either way, so an inline
			// removal never has the :exec problem a named statement can
			// have — and the check must not treat it as though it might.
			if !statement.CountIsReachable() {
				t.Error("the count of an inline removal is reported as unreachable; " +
					"the driver returns it whatever the SQL was written in")
			}
		})
	}
}

// TestInlineWritesReadBothLiteralSpellings pins the source-level half: the
// text has to come out of either spelling of a Go string, because the
// short writes are quoted and the wrapped ones are raw.
func TestInlineWritesReadBothLiteralSpellings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "interpreted", value: `"UPDATE t SET enabled = FALSE"`, want: "UPDATE t SET enabled = FALSE"},
		{name: "raw", value: "`UPDATE t\n\tSET enabled = FALSE`", want: "UPDATE t\n\tSET enabled = FALSE"},
	} {
		got, ok := GoStringLiteral(tc.value)
		if !ok || got != tc.want {
			t.Errorf("%s: read %q (ok=%v), want %q", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := GoStringLiteral("42"); ok {
		t.Error("a non-string literal was read as SQL")
	}
}
