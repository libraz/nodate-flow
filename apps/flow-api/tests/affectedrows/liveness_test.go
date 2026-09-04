package affectedrows

import (
	"sort"
	"strings"
	"testing"
)

// TestEveryLivenessColumnIsClassified refuses a schema column that could
// carry liveness and that the removal derivation has never been told
// about.
//
// The derivation reads the removal shape in a fixed set of columns. A
// column added outside that set does not break anything visibly: the
// statements written in it simply stop being checked, while the not-found
// answer they owe their callers is unchanged. Requiring every candidate to
// be classified turns that silence into a failure, at the point where
// somebody still knows what the new column means.
//
// Nothing is listed on this side. The candidates come out of the schema by
// shape; the classification is what is written by hand.
func TestEveryLivenessColumnIsClassified(t *testing.T) {
	t.Parallel()

	candidates := schemaCandidates(t)
	for _, candidate := range candidates {
		role, ok := RoleFor(candidate.Table, candidate.Column)
		if !ok {
			t.Errorf("%s.%s is shaped so it can hold a row out of the live set, and the "+
				"removal derivation does not know it. Until it is classified in liveness.go, "+
				"an UPDATE that removes a row through this column reads as an ordinary patch "+
				"and its affected-row count goes unchecked. Give it %q when the reads "+
				"restrict on it and nothing puts it back, or one of the other roles with the "+
				"reason it is not a removal",
				candidate.Table, candidate.Column, RemovalMarker)
			continue
		}
		// A generated column is not assignable, so no statement can carry
		// a removal in it. That is a property of the schema rather than a
		// judgement, so the two are held against each other: the role may
		// not be claimed for a column the schema lets a writer assign,
		// and a column the schema generates may not be given any other.
		if candidate.Generated != (role == DerivedMarker) {
			t.Errorf("%s.%s is classified %q and the schema %s it. A column a statement can "+
				"assign needs a role that says what writing it does to the row; a generated "+
				"one needs %q, because nothing can write it at all",
				candidate.Table, candidate.Column, role,
				map[bool]string{true: "generates", false: "does not generate"}[candidate.Generated],
				DerivedMarker)
		}
	}
}

// TestLivenessClassificationCoversOnlyTheSchema drops a classification
// that has outlived its column.
//
// A vocabulary entry is a claim about a column in this schema. Once the
// column is gone the entry stops being about anything, and a reader who
// finds it there concludes the question was settled for a column that no
// longer exists — which is how the removal markers would come to be
// believed rather than checked.
func TestLivenessClassificationCoversOnlyTheSchema(t *testing.T) {
	t.Parallel()

	bare := map[string]bool{}
	qualified := map[string]bool{}
	for _, candidate := range schemaCandidates(t) {
		bare[candidate.Column] = true
		qualified[candidate.Qualified()] = true
	}
	for entry := range ClassifiedEntries() {
		table, column := splitQualified(entry)
		if table == "" {
			if !bare[column] {
				t.Errorf("liveness.go classifies %q, which no table declares in a shape that "+
					"could carry liveness. Drop the entry", entry)
			}
			continue
		}
		if !qualified[entry] {
			t.Errorf("liveness.go classifies %q, and %s carries no such column in a shape "+
				"that could hold a row out of the live set. Drop the entry", entry, table)
		}
	}
}

// TestQualifiedClassificationsRestOnEvidence drops a per-table entry that
// decides nothing, or that decides something the queries contradict.
//
// A qualified entry is the one place the vocabulary can say two tables
// carrying the same column name mean different things by it, and that is
// exactly where a hand-written list would start drifting from the SQL. So
// it has to clear two bars. It has to disagree with the bare entry, or it
// changes no outcome and only reads as though a question was settled. And
// the tables have to actually differ in the derivation: the column is
// cleared on one and never on the other, which is what makes one of them a
// state the row leaves and the other a removal.
//
// The second bar is a veto rather than a proof. Nothing in the SQL
// separates a read that excludes a row from one that selects a facet of
// it — the unread counts restrict on read_at exactly as the inbox
// restricts on archived_at — so which side of the line a column falls on
// stays a judgement. What this removes is the ability to make that
// judgement against the evidence: the day something un-archives a
// notification, the entry saying archiving ends it fails.
//
// One shape stays outside the veto. Where a table never clears the column
// and no read of that table restricts on it, the clearing evidence differs
// from the other tables for a reason that has nothing to do with removal —
// the column is inert there — and an entry calling it a removal marker
// passes. Only the reads would tell them apart, and a read that excludes a
// row is indistinguishable from one that selects a facet, which is the
// same limit as above. Such an entry has to be argued, not just accepted
// because the check is green.
func TestQualifiedClassificationsRestOnEvidence(t *testing.T) {
	t.Parallel()

	revived := RevivedColumns(allStatements(t))
	for entry, role := range ClassifiedEntries() {
		table, column := splitQualified(entry)
		if table == "" {
			continue
		}
		general, ok := bareRoles[column]
		if !ok {
			// Nothing to disagree with: the column is classified only
			// here, and the candidate check covers the other tables.
			continue
		}
		if general == role {
			t.Errorf("%q classifies %s as %q, which is what %q already says. The entry "+
				"changes nothing and reads as a considered exception; drop it, or state the "+
				"role this table actually differs on", entry, column, role, column)
			continue
		}
		clearing := ClearingTables(revived, column)
		elsewhere := false
		for other := range clearing {
			if other != table {
				elsewhere = true
			}
		}
		if clearing[table] == elsewhere {
			t.Errorf("%q classifies %s as %q against %q everywhere else, and the queries draw "+
				"no such line: %s is %s on this table and %s on the others. Either the "+
				"distinction is not the one the SQL makes, or the writer that made it has "+
				"gone", entry, column, role, general, column,
				clearedWord(clearing[table]), clearedWord(elsewhere))
		}
	}
}

func clearedWord(cleared bool) string {
	if cleared {
		return "written back to NULL"
	}
	return "never written back to NULL"
}

// TestRemovalMarkersAreClassifiedOnce pins the two properties the removal
// derivation depends on: a marker names a real column, and no column is
// classified twice under conflicting roles.
func TestRemovalMarkersAreClassifiedOnce(t *testing.T) {
	t.Parallel()

	seen := map[string]LivenessRole{}
	for _, group := range livenessVocabulary {
		if strings.TrimSpace(group.Reason) == "" {
			t.Errorf("the %q group states no reason, so nothing records why these columns "+
				"were placed outside the removal shape", group.Role)
		}
		for _, column := range group.Columns {
			if previous, ok := seen[column]; ok {
				t.Errorf("%q is classified both %q and %q; the removal derivation would "+
					"read whichever came first", column, previous, group.Role)
				continue
			}
			seen[column] = group.Role
		}
	}
	if len(RemovalMarkerColumns()) == 0 {
		t.Fatal("no column is classified as a removal marker, so the soft-delete shape " +
			"matches nothing and every check in this package passes vacuously")
	}
}

// TestRemovalMarkersAreNeverWrittenBack pins the rule that decides which
// side of the vocabulary a tombstone column falls on.
//
// A removal marker and a reversible state are written the same way — a
// guarded UPDATE onto a column the reads restrict on — and the SET clause
// alone cannot tell them apart. What tells them apart is whether anything
// puts the row back: archiving has a writer that clears the column, so a
// zero count there reports a row that was already archived and is still
// reachable, while a removal has none and a zero count is the caller's
// 404.
//
// That distinction decides whether every statement written in a column is
// checked at all, and it lives in prose in the classification. This makes
// it hold mechanically: the day something starts clearing a removal
// marker, the marker has become a reversible state and the entry saying
// otherwise fails.
func TestRemovalMarkersAreNeverWrittenBack(t *testing.T) {
	t.Parallel()

	revived := RevivedColumns(allStatements(t))
	if len(revived) == 0 {
		t.Fatal("no statement under sql/queries writes any column back to NULL; the scan " +
			"that separates a reversible state from a removal has stopped matching, and " +
			"every marker passes this check without being examined")
	}
	for key, statements := range revived {
		table, column := splitQualified(key)
		if role, ok := RoleFor(table, column); !ok || role != RemovalMarker {
			continue
		}
		for _, s := range statements {
			t.Errorf("%s: %s writes %s back to NULL, so a row in that state comes back into "+
				"the reads and the column is a state the row leaves rather than a removal. "+
				"Reclassify it in liveness.go: while it is a removal marker, every guarded "+
				"UPDATE onto it is treated as owing the caller a not-found answer it does "+
				"not owe", s.Location(), s.Name, key)
		}
	}
}

// TestLivenessCandidateShapesAreRead is the positive control for the
// schema side. It proves the candidate scan reads the shapes it claims to
// read, rather than the vocabulary check passing because the scan found
// nothing to ask about.
func TestLivenessCandidateShapesAreRead(t *testing.T) {
	t.Parallel()

	const schema = "CREATE TABLE t (\n" +
		"  id INT UNSIGNED NOT NULL AUTO_INCREMENT,\n" +
		"  public_id BINARY(16) NOT NULL COMMENT 'UUID v7',\n" +
		"  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Soft-delete flag (0 = deleted, 1 = live)',\n" +
		"  deleted_at DATETIME(3) NULL DEFAULT NULL COMMENT 'Removed at (NULL, TRUE) while live',\n" +
		"  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),\n" +
		"  retries TINYINT UNSIGNED NOT NULL DEFAULT 0,\n" +
		"  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL,\n" +
		"  slug_key VARCHAR(64) GENERATED ALWAYS AS (LOWER(slug)) STORED NOT NULL,\n" +
		"  PRIMARY KEY (id),\n" +
		"  UNIQUE KEY uq_t_slug (slug_key, active)\n" +
		");\n" +
		"CREATE TABLE u (\n" +
		"  deleted_at DATETIME(3) NULL COMMENT 'Second table carrying the same name'\n" +
		");\n"

	var got []string
	generated := map[string]bool{}
	for _, candidate := range LivenessCandidates(schema) {
		got = append(got, candidate.Qualified())
		if candidate.Generated {
			generated[candidate.Qualified()] = true
		}
	}
	// The flag and the nullable timestamp qualify; the NOT NULL timestamp,
	// the unsigned counter and the identifiers do not, and the generated
	// column qualifies only because its expression names a removal marker.
	// The same name on a second table is a candidate of its own, because
	// what the reads of that table do with it is a separate question.
	want := []string{"t.active", "t.deleted_at", "t.enabled", "u.deleted_at"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("candidates are %v, want %v", got, want)
	}
	if !generated["t.active"] {
		t.Error("active was read as a written column, so a generated projection of a " +
			"removal marker would be demanded a classification it can never need")
	}
}

// TestRoleResolvesPerTable is the control for the split itself: one column
// name, two tables, two roles.
//
// This is what the whole per-table key buys, and it is the part that
// silently stops working if resolution ever falls back to the bare entry
// first. So it is pinned on the committed vocabulary rather than on a
// sample: archiving ends a notification and only shelves a task, and the
// derivation has to read the same SET clause two different ways depending
// on which table it writes.
func TestRoleResolvesPerTable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		table  string
		column string
		want   LivenessRole
	}{
		{"notifications", "archived_at", RemovalMarker},
		{"tasks", "archived_at", ReversibleState},
		{"pages", "archived_at", ReversibleState},
		{"calendar_event_comments", "deleted_at", RemovalMarker},
		{"tasks", "enabled", RemovalMarker},
	} {
		got, ok := RoleFor(tc.table, tc.column)
		if !ok {
			t.Errorf("%s.%s is unclassified, want %q", tc.table, tc.column, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%s.%s resolves to %q, want %q", tc.table, tc.column, got, tc.want)
		}
	}

	// The same statement text has to classify both ways, or the role is
	// being read and then ignored.
	const src = `-- name: ArchiveNote :execrows
UPDATE notifications
SET archived_at = NOW()
WHERE public_id = ?
  AND archived_at IS NULL;

-- name: ArchiveTask :execrows
UPDATE tasks
SET archived_at = CURRENT_TIMESTAMP
WHERE public_id = ?
  AND archived_at IS NULL;
`
	got := parseFile("sample.sql", src)
	if len(got) != 2 {
		t.Fatalf("parsed %d statements, want 2", len(got))
	}
	if kind := got[0].RemovalKind(); kind != SoftDelete {
		t.Errorf("the notifications statement is classified %q, want %q", kind, SoftDelete)
	}
	if kind := got[1].RemovalKind(); kind != NotRemoval {
		t.Errorf("the tasks statement is classified %q, want %q", kind, NotRemoval)
	}
}

// schemaCandidates reads sql/schema.sql and returns the liveness-shaped
// columns, failing when there are none: this check reads a file by path,
// and a scan that has stopped matching passes for the wrong reason.
func schemaCandidates(t *testing.T) []LivenessCandidate {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	schema, err := ReadSchema(root)
	if err != nil {
		t.Fatalf("read sql/schema.sql: %v", err)
	}
	candidates := LivenessCandidates(schema)
	if len(candidates) == 0 {
		t.Fatal("no column in sql/schema.sql carries a liveness shape; the schema scan has " +
			"stopped matching rather than the columns having gone away")
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Qualified() < candidates[j].Qualified()
	})
	return candidates
}
