package duplicaterefusal

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestAttributionReadsEachBindingShape is the positive control for the
// half of this check that has to be right for the other half to mean
// anything.
//
// A derivation that stops matching places nothing and reports nothing,
// which is indistinguishable from a tree with nothing to report. So the
// walk is pointed at a tree holding every shape a duplicate-entry branch is
// written in here — a plain assignment above the branch, the assignment
// carried in the `if` that tests it, a switch case reading an error bound
// further up, and a statement reached through a function value handed to a
// retry helper — plus the shapes it must refuse to guess at.
func TestAttributionReadsEachBindingShape(t *testing.T) {
	t.Parallel()

	root := writeControlTree(t)
	statements, err := Statements(root)
	if err != nil {
		t.Fatalf("read control queries: %v", err)
	}
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}
	placed, unresolved := Attribute(src, statements)

	gotPlaced := map[string]string{}
	for _, a := range placed {
		gotPlaced[funcName(a.Branch.Func)] = a.Table
	}
	wantPlaced := map[string]string{
		"assignsThenBranches":     "widgets",
		"bindsInTheIfStatement":   "widget_tags",
		"readsErrorInSwitchCase":  "blobs",
		"issuesThroughAHelper":    "ledger",
		"branchesOnAnIdentityKey": "notes",
	}
	for name, want := range wantPlaced {
		got, ok := gotPlaced[name]
		if !ok {
			t.Errorf("%s was not placed; the derivation no longer reads that binding shape", name)
			continue
		}
		if got != want {
			t.Errorf("%s was attributed to %q, want %q", name, got, want)
		}
	}
	for name := range gotPlaced {
		if _, want := wantPlaced[name]; !want {
			t.Errorf("%s was placed, but its write is not one the derivation can honestly name", name)
		}
	}

	var gotMissed []string
	for _, u := range unresolved {
		gotMissed = append(gotMissed, funcName(u.Branch.Func))
	}
	sort.Strings(gotMissed)
	wantMissed := []string{"branchesOnAReadsError", "branchesOnTwoTables", "takesTheErrorAsAParameter"}
	if strings.Join(gotMissed, ",") != strings.Join(wantMissed, ",") {
		t.Errorf("the derivation reported %v as unresolvable, want %v; a site it guesses at and a site it "+
			"silently drops are the same failure wearing different clothes", gotMissed, wantMissed)
	}
}

// TestCollidableKeysExcludeTheIdentifierKeys pins what makes a table able
// to raise the conflict a named refusal describes.
//
// The distinction the whole check rests on is between a key over the row's
// own generated identifier and a key over what the caller sent. A key
// holding public_id is violated only when a UUID repeats, whatever else it
// holds — so scoping one by workspace does not turn it into a business key,
// and a key that merely mentions some other table's public id in a column
// of its own is not an identifier key at all.
func TestCollidableKeysExcludeTheIdentifierKeys(t *testing.T) {
	t.Parallel()

	root := writeControlTree(t)
	tables, err := ReadTables(root)
	if err != nil {
		t.Fatalf("read control tables: %v", err)
	}

	for _, want := range []struct {
		table      string
		collidable []string
	}{
		{table: "widgets", collidable: []string{"uniq_widgets_workspace_name (workspace_id, name)"}},
		{table: "notes", collidable: nil},
		{table: "blobs", collidable: []string{"uniq_blobs_sha (workspace_id, sha256)"}},
		{table: "ledger", collidable: []string{"uniq_ledger_reverses (workspace_id, reverses_id)"}},
		{table: "widget_tags", collidable: []string{"uniq_widget_tags_pair (widget_id, tag_id, active)"}},
		// The foreign public id of another row is a value the caller sends,
		// so a key over it is one the caller can collide.
		{table: "favourites", collidable: []string{"uniq_favourites_target (user_id, target_public_id)"}},
	} {
		table, ok := tables[want.table]
		if !ok {
			t.Errorf("the control schema declares %s but the reader did not find it", want.table)
			continue
		}
		got := renderKeys(table.Collidable())
		expected := "none"
		if len(want.collidable) > 0 {
			expected = strings.Join(want.collidable, ", ")
		}
		if got != expected {
			t.Errorf("%s: collidable keys are %s, want %s", want.table, got, expected)
		}
	}
}

// TestAttributionExceptionsCannotOutliveTheirSite proves the three ways an
// exemption rots are all refused.
//
// The list is the record of what this check does not read, so it is checked
// the way the code is: an entry naming a file that is gone, an entry with
// nothing after the reason, and an entry covering a site that no longer
// fails are each a failure, whether or not the committed list happens to
// hold one today.
func TestAttributionExceptionsCannotOutliveTheirSite(t *testing.T) {
	t.Parallel()

	root, _, _, _, unresolved := load(t)

	// The branch this control reasons about has to sit in a file that is
	// really there, because "the file is gone" is one of the three failures
	// under test and the other two must not accidentally trip it.
	live := Branch{
		File: "apps/flow-api/tests/duplicaterefusal/exceptions.go",
		Func: "apps/flow-api/tests/duplicaterefusal.createSomething",
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(live.File))); err != nil {
		t.Fatalf("the control needs a file that exists to reason about: %v", err)
	}
	present := AttributionException{
		File:   live.File,
		Func:   live.Func,
		Reason: "the write is issued by a helper this walk does not follow",
	}

	for _, tc := range []struct {
		name       string
		exception  AttributionException
		unresolved []Unresolved
		want       string
	}{
		{
			name:       "a file that is not in the repository",
			exception:  AttributionException{File: "apps/flow-api/internal/gone/write.go", Func: live.Func, Reason: "stated"},
			unresolved: []Unresolved{{Branch: live}},
			want:       "not a file",
		},
		{
			name:       "a reason that says nothing",
			exception:  AttributionException{File: present.File, Func: live.Func, Reason: "   "},
			unresolved: []Unresolved{{Branch: live}},
			want:       "states no reason",
		},
		{
			name:       "a site that no longer fails to resolve",
			exception:  present,
			unresolved: nil,
			want:       "covers no branch",
		},
	} {
		problem := tc.exception.Problem(root, tc.unresolved)
		if !strings.Contains(problem, tc.want) {
			t.Errorf("%s was reported as %q, want something naming %q", tc.name, problem, tc.want)
		}
	}

	// The same entry, covering a site that is genuinely unresolved, has to
	// pass — an exemption nothing can satisfy is a permanent failure rather
	// than a record.
	if problem := present.Problem(root, []Unresolved{{Branch: live}}); problem != "" {
		t.Errorf("a sound exception was rejected as %q", problem)
	}

	// And the committed list is held to the same rule against the real
	// tree, so this control cannot pass while the list itself has rotted.
	for _, e := range AttributionExceptions {
		if problem := e.Problem(root, unresolved); problem != "" {
			t.Errorf("the committed exception for %s in %s %s", e.Func, e.File, problem)
		}
	}
}

// funcName reduces a package-qualified function to its own name.
func funcName(qualified string) string {
	return qualified[strings.LastIndex(qualified, ".")+1:]
}

// writeControlTree lays out a minimal repository holding one table of each
// key shape, one statement per write, and one function per binding shape,
// and returns the root the readers would be given.
func writeControlTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("sql/flow/tables/widgets.sql", `
CREATE TABLE widgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL,
  name VARCHAR(64) NOT NULL COMMENT 'Display name, UNIQUE in prose only, with a comma',
  UNIQUE KEY uniq_widgets_public_id (public_id),
  UNIQUE KEY uniq_widgets_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_widgets_workspace_name (workspace_id, name),
  KEY idx_widgets_name (name)
) ENGINE=InnoDB;

CREATE TABLE notes (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  public_id BINARY(16) NOT NULL,
  workspace_id INT UNSIGNED NOT NULL,
  title VARCHAR(500) NOT NULL,
  UNIQUE KEY uniq_notes_public_id (public_id),
  UNIQUE KEY uniq_notes_workspace_public_id (workspace_id, public_id),
  FULLTEXT KEY ft_notes_title (title)
) ENGINE=InnoDB;

CREATE TABLE widget_tags (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  public_id BINARY(16) NOT NULL,
  widget_id INT UNSIGNED NOT NULL,
  tag_id INT UNSIGNED NOT NULL,
  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL,
  UNIQUE KEY uniq_widget_tags_public_id (public_id),
  UNIQUE KEY uniq_widget_tags_pair (widget_id, tag_id, active)
) ENGINE=InnoDB;

CREATE TABLE favourites (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  public_id BINARY(16) NOT NULL,
  user_id INT UNSIGNED NOT NULL,
  target_public_id BINARY(16) NOT NULL,
  UNIQUE KEY uniq_favourites_public_id (public_id),
  UNIQUE KEY uniq_favourites_target (user_id, target_public_id)
) ENGINE=InnoDB;

CREATE TABLE ledger (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  public_id BINARY(16) NOT NULL,
  workspace_id INT UNSIGNED NOT NULL,
  reverses_id BIGINT UNSIGNED NULL,
  UNIQUE KEY uniq_ledger_public_id (public_id),
  UNIQUE KEY uniq_ledger_reverses (workspace_id, reverses_id)
) ENGINE=InnoDB;
`)

	// The blob table gets its business key by ALTER, which is the shape a
	// reader that only knows CREATE TABLE would miss.
	write("sql/flow/tables/blobs.sql", `
CREATE TABLE blobs (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  public_id BINARY(16) NOT NULL,
  workspace_id INT UNSIGNED NULL,
  sha256 BINARY(32) NOT NULL,
  UNIQUE KEY uniq_blobs_public_id (public_id)
) ENGINE=InnoDB;
`)
	write("sql/flow/constraints/uniq_blobs_sha.sql", `
ALTER TABLE blobs ADD UNIQUE KEY uniq_blobs_sha (workspace_id, sha256);
`)

	write("sql/queries/control/control.sql", `
-- name: CreateWidget :execlastid
INSERT INTO widgets (public_id, workspace_id, name) VALUES (?, ?, ?);

-- name: CreateNote :execlastid
INSERT INTO notes (public_id, workspace_id, title) VALUES (?, ?, ?);

-- name: TagWidget :execlastid
INSERT INTO widget_tags (public_id, widget_id, tag_id) VALUES (?, ?, ?);

-- name: InsertBlob :execresult
INSERT INTO blobs (public_id, workspace_id, sha256) VALUES (?, ?, ?);

-- name: AppendLedger :execlastid
INSERT INTO ledger (public_id, workspace_id, reverses_id) VALUES (?, ?, ?);

-- name: UpdateWidget :execrows
UPDATE widgets SET name = ? WHERE workspace_id = ? AND public_id = ?;

-- name: FindWidget :one
SELECT id FROM widgets WHERE workspace_id = ? AND public_id = ?;
`)

	write("apps/flow-api/internal/control/writes.go", `package control

// assignsThenBranches is the ordinary shape: the statement's error is
// assigned, then tested on the next line.
func assignsThenBranches(ctx Ctx, q Q) error {
	_, err := q.CreateWidget(ctx, nil)
	if err != nil {
		if isDuplicateEntry(err) {
			return ErrTaken
		}
		return err
	}
	return nil
}

// bindsInTheIfStatement carries the assignment in the if that tests it.
func bindsInTheIfStatement(ctx Ctx, q Q) error {
	if _, err := q.TagWidget(ctx, nil); err != nil {
		if isDuplicateEntry(err) {
			return ErrTaken
		}
		return err
	}
	return nil
}

// readsErrorInSwitchCase tests an error bound further up, inside a switch.
func readsErrorInSwitchCase(ctx Ctx, q Q) error {
	res, insErr := q.InsertBlob(ctx, nil)
	switch {
	case insErr == nil:
		_ = res
		return nil
	case IsDuplicateEntry(insErr):
		return ErrRace
	default:
		return insErr
	}
}

// issuesThroughAHelper closes the statement over and lets the helper decide
// whether to re-issue it, so the binding names the helper and the write is
// one level in.
func issuesThroughAHelper(ctx Ctx, q Q, db DB) error {
	insert := func(ctx Ctx) error {
		_, err := q.AppendLedger(ctx, nil)
		return err
	}
	err := db.RunStatement(ctx, "control.append", insert)
	if err != nil {
		if isDuplicateEntry(err) {
			return ErrAlreadyReversed
		}
		return err
	}
	return nil
}

// branchesOnAnIdentityKey writes a table whose only unique keys are over
// the generated public_id, which is the defect this package refuses.
func branchesOnAnIdentityKey(ctx Ctx, q Q) error {
	if _, err := q.CreateNote(ctx, nil); err != nil {
		if isDuplicateEntry(err) {
			return ErrTaken
		}
		return err
	}
	return nil
}

// branchesOnAReadsError tests the error of a SELECT, which raises no
// duplicate key.
func branchesOnAReadsError(ctx Ctx, q Q) error {
	if _, err := q.FindWidget(ctx, nil); err != nil {
		if isDuplicateEntry(err) {
			return ErrTaken
		}
		return err
	}
	return nil
}

// branchesOnTwoTables binds one error to a call performing writes on two
// tables, so no single refusal describes it.
func branchesOnTwoTables(ctx Ctx, q Q) error {
	err := both(func(ctx Ctx) error {
		if _, e := q.CreateWidget(ctx, nil); e != nil {
			return e
		}
		_, e := q.CreateNote(ctx, nil)
		return e
	})
	if isDuplicateEntry(err) {
		return ErrTaken
	}
	return err
}

// takesTheErrorAsAParameter is handed the error from somewhere else, so no
// assignment in this function names the write behind it.
func takesTheErrorAsAParameter(err error) error {
	if isDuplicateEntry(err) {
		return ErrTaken
	}
	return err
}
`)

	return root
}
