package stream

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// The tailer's whole job is to observe rows this process did not
// write, which no fake can reproduce: the interesting cases are about
// when MySQL makes an AUTO_INCREMENT id visible. These tests run
// against a real database.
var tailShared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "stream_tail_test",
})

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func tailDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("tailer tests require MySQL; skipping in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := tailShared.Start(ctx)
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	return inst.DB
}

// recorder captures what the tailer published instead of fanning it out.
type recorder struct {
	NopNotifier
	events []Event
}

func (r *recorder) Publish(_ context.Context, evt Event) {
	r.events = append(r.events, evt)
}

func (r *recorder) kinds() []Kind {
	out := make([]Kind, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

// seedWorkspace creates a workspace and returns (internal id, public id).
func seedWorkspace(t *testing.T, db *sql.DB, slug string) (uint32, string) {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO workspaces (public_id, name, slug)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?)`, slug, slug)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	var pub string
	if err := db.QueryRow(
		`SELECT BIN_TO_UUID(public_id, 0) FROM workspaces WHERE id = ?`, id,
	).Scan(&pub); err != nil {
		t.Fatalf("workspace public id: %v", err)
	}
	//#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	return uint32(id), pub
}

// appendEvent writes one row the way another process would: a plain
// INSERT with no notify hook behind it.
func appendEvent(t *testing.T, x interface {
	Exec(string, ...any) (sql.Result, error)
}, wsID uint32, eventType string) uint64 {
	t.Helper()
	res, err := x.Exec(`
		INSERT INTO events (public_id, workspace_id, type, payload_json, occurred_at)
		VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, JSON_OBJECT(), NOW(3))`, wsID, eventType)
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("event id: %v", err)
	}
	//#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	return uint64(id)
}

// newTestTailer builds a tailer already positioned at the end of the
// log, so a test only sees the rows it writes itself.
func newTestTailer(t *testing.T, db *sql.DB, rec *recorder, tap *EventbusTap) *Tailer {
	t.Helper()
	tl := NewTailer(db, rec, tap)
	tl.grace = time.Hour // promotion is driven explicitly per test
	if err := tl.seekToEnd(context.Background()); err != nil {
		t.Fatalf("seek: %v", err)
	}
	return tl
}

func TestTailerPublishesAnotherProcessesAppends(t *testing.T) {
	db := tailDB(t)
	wsID, wsPub := seedWorkspace(t, db, "tail-foreign")

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	appendEvent(t, db, wsID, "calendar.event.created")

	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("want 1 published event, got %d (%v)", len(rec.events), rec.kinds())
	}
	if rec.events[0].Kind != KindCalendarChanged {
		t.Errorf("kind = %q, want %q", rec.events[0].Kind, KindCalendarChanged)
	}
	if rec.events[0].WorkspaceID != wsPub {
		t.Errorf("workspace = %q, want %q", rec.events[0].WorkspaceID, wsPub)
	}
}

// A restarting process must not replay the log as invalidations: every
// subscriber already gets a resync marker when it connects.
func TestTailerStartsAtTheEndOfTheLog(t *testing.T) {
	db := tailDB(t)
	wsID, _ := seedWorkspace(t, db, "tail-history")

	for range 3 {
		appendEvent(t, db, wsID, "calendar.event.created")
	}

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(rec.events) != 0 {
		t.Fatalf("history was replayed: %d events (%v)", len(rec.events), rec.kinds())
	}
}

// The tap and the tailer read the same log, so without a ledger every
// local write would invalidate twice.
func TestTailerSkipsWhatThisProcessAlreadyPublished(t *testing.T) {
	db := tailDB(t)
	wsID, wsPub := seedWorkspace(t, db, "tail-self")

	rec := &recorder{}
	tap := NewEventbusTap(rec)
	tap.RememberWorkspace(wsID, wsPub)
	tl := newTestTailer(t, db, rec, tap)

	// The way a local append actually happens: the row is written, then
	// the commit hook fires the tap.
	own := appendEvent(t, db, wsID, "calendar.event.created")
	tap.Publish(context.Background(), wsID, "calendar.event.created", own)
	if len(rec.events) != 1 {
		t.Fatalf("tap should have published once, got %d", len(rec.events))
	}

	// Something else wrote this one.
	appendEvent(t, db, wsID, "calendar.event.updated")

	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if len(rec.events) != 2 {
		t.Fatalf("want 2 events total (1 tap + 1 tailed), got %d (%v)",
			len(rec.events), rec.kinds())
	}
	if tl.skipped.Load() != 1 {
		t.Errorf("skipped = %d, want 1", tl.skipped.Load())
	}
}

// The load-bearing case. AUTO_INCREMENT hands out an id when the
// INSERT runs, but the row is invisible until its transaction commits.
// A tailer that advanced straight to the highest id it had seen would
// step over the lower id permanently.
func TestTailerCatchesARowThatCommitsAfterAHigherID(t *testing.T) {
	db := tailDB(t)
	wsID, _ := seedWorkspace(t, db, "tail-late-commit")

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	// Take the lower id inside a transaction and hold it open.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	lateID := appendEvent(t, tx, wsID, "calendar.event.created")

	// A second writer takes a higher id and commits immediately.
	earlyID := appendEvent(t, db, wsID, "calendar.event.updated")
	if lateID >= earlyID {
		t.Fatalf("fixture did not produce the interleaving: late=%d early=%d", lateID, earlyID)
	}

	// The tailer reads while the lower id is still uncommitted.
	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if len(rec.events) != 1 {
		t.Fatalf("want the committed row only, got %d (%v)", len(rec.events), rec.kinds())
	}

	// Now the earlier transaction lands.
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	if len(rec.events) != 2 {
		t.Fatalf("the late-committing row was skipped: %d events (%v)",
			len(rec.events), rec.kinds())
	}
}

// Re-reading below the high-water mark is what catches a late commit,
// so it must not also re-publish rows that were already delivered.
func TestTailerDoesNotRepublishWhileRescanning(t *testing.T) {
	db := tailDB(t)
	wsID, _ := seedWorkspace(t, db, "tail-rescan")

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	appendEvent(t, db, wsID, "calendar.event.created")
	for range 3 {
		if _, err := tl.poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	if len(rec.events) != 1 {
		t.Fatalf("re-scan republished: %d events (%v)", len(rec.events), rec.kinds())
	}
}

// Once a row has had its grace period to commit, the floor moves past
// it and the suppression set releases it — otherwise both would grow
// for the life of the process.
func TestTailerAdvancesPastRowsOlderThanTheGrace(t *testing.T) {
	db := tailDB(t)
	wsID, _ := seedWorkspace(t, db, "tail-promote")

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	id := appendEvent(t, db, wsID, "calendar.event.created")
	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	// Two promotions: the first makes the id pending, the second makes
	// it safe. Both are needed — a single one would move the floor to a
	// mark taken before the row existed.
	tl.grace = 0
	tl.promoteAt = time.Time{}
	tl.promote()
	tl.promote()

	if tl.safeID < id {
		t.Errorf("safeID = %d, want at least %d", tl.safeID, id)
	}
	if _, held := tl.seen[id]; held {
		t.Errorf("id %d still held in the suppression set after promotion", id)
	}
}

// An event type outside the published families is not something the
// frontend can act on, so it must not wake every query in a workspace.
func TestTailerIgnoresUnknownEventTypes(t *testing.T) {
	db := tailDB(t)
	wsID, _ := seedWorkspace(t, db, "tail-unknown")

	rec := &recorder{}
	tl := newTestTailer(t, db, rec, nil)

	appendEvent(t, db, wsID, "internal.housekeeping.ran")

	if _, err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(rec.events) != 0 {
		t.Fatalf("unknown type was published: %v", rec.kinds())
	}
}
