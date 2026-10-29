package storagegc_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// The tests here run a whole-instance pass against a database only this
// package uses, so none of them is parallel: one test's pass would reclaim
// what another is still planting.

// recordingRemover stands in for the object store and keeps every key it
// was asked to remove.
type recordingRemover struct {
	mu   sync.Mutex
	keys []string
}

func (r *recordingRemover) RemoveObject(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	return nil
}

func (r *recordingRemover) removed(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.keys, key)
}

func newTestSweeper(db *sql.DB, store storagegc.ObjectRemover) *storagegc.Sweeper {
	return storagegc.New(db, generated.New(db), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func lastID(t *testing.T, res sql.Result) uint32 {
	t.Helper()
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- AUTO_INCREMENT LastInsertId in a test seed, fits uint32
}

// seedWorkspace creates a workspace and an uploader for workspace-scoped
// objects and attachment rows.
func seedWorkspace(t *testing.T, db *sql.DB) (wsID, userID uint32) {
	t.Helper()
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	wsPub, userPub := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "sweep-"+tag, "Sweep "+tag)
	require.NoError(t, err)
	wsID = lastID(t, res)
	res, err = db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "sweep-"+tag+"@example.test", "Sweep "+tag)
	require.NoError(t, err)
	return wsID, lastID(t, res)
}

// object describes a storage_objects row to plant.
type object struct {
	confirmed bool
	refCount  int
	age       time.Duration // subtracted from now for created_at and updated_at
}

// plantObject inserts a workspace-scoped storage object and returns its id
// and key.
func plantObject(t *testing.T, db *sql.DB, wsID uint32, o object) (uint32, string) {
	t.Helper()
	sha := make([]byte, 32)
	_, err := rand.Read(sha)
	require.NoError(t, err)
	pub := uuid.Must(uuid.NewV7())
	key := "sweep-test/" + pub.String()
	at := time.Now().UTC().Add(-o.age)
	var uploadedAt sql.NullTime
	if o.confirmed {
		uploadedAt = sql.NullTime{Time: at, Valid: true}
	}
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO storage_objects
		   (public_id, workspace_id, sha256, byte_size, content_type, storage_key, ref_count, uploaded_at, created_at, updated_at)
		 VALUES (?, ?, ?, 1, 'application/octet-stream', ?, ?, ?, ?, ?)`,
		pub[:], wsID, sha, key, o.refCount, uploadedAt, at, at)
	require.NoError(t, err)
	return lastID(t, res), key
}

// plantTaskAttachment points a flow-layer attachment row at an object, the
// shape a presign leaves behind.
func plantTaskAttachment(t *testing.T, db *sql.DB, wsID, userID, objectID uint32) {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO attachments (public_id, workspace_id, uploader_id, storage_object_id, filename)
		 VALUES (?, ?, ?, ?, 'sweep.bin')`,
		pub[:], wsID, userID, objectID)
	require.NoError(t, err)
}

// createForeignTable stands in for a product layer this sweeper knows
// nothing about: a table holding storage_objects(id) with ON DELETE
// RESTRICT, as nodate-time's album_photos does.
func createForeignTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	name := "sweep_foreign_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	//#nosec G202 -- name is generated above, not input
	_, err := db.ExecContext(ctx, `CREATE TABLE `+name+` (
		id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		storage_object_id INT UNSIGNED NOT NULL,
		CONSTRAINT fk_`+name+`_storage_object FOREIGN KEY (storage_object_id)
		  REFERENCES storage_objects(id) ON DELETE RESTRICT
	) ENGINE=InnoDB`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE `+name) }) //#nosec G202 -- generated name
	return name
}

func plantForeignRow(t *testing.T, db *sql.DB, table string, objectID uint32) {
	t.Helper()
	//#nosec G202 -- table is a name createForeignTable generated
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO `+table+` (storage_object_id) VALUES (?)`, objectID)
	require.NoError(t, err)
}

func objectExists(t *testing.T, db *sql.DB, id uint32) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM storage_objects WHERE id = ?`, id).Scan(&n))
	return n > 0
}

func taskAttachmentCount(t *testing.T, db *sql.DB, objectID uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM attachments WHERE storage_object_id = ?`, objectID).Scan(&n))
	return n
}

// TestSweeperForeignRestrictSkipsUnreferencedObject: an object whose
// ref_count says nothing points at it, while a row in another layer
// still does. The RESTRICT edge is the evidence the count is wrong; the
// sweeper skips the object and goes on to the next one.
func TestSweeperForeignRestrictSkipsUnreferencedObject(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	wsID, _ := seedWorkspace(t, db)
	foreign := createForeignTable(t, db)

	// Older, so the pass reaches it before the control row.
	held, heldKey := plantObject(t, db, wsID, object{confirmed: true, age: 2 * time.Hour})
	plantForeignRow(t, db, foreign, held)
	free, freeKey := plantObject(t, db, wsID, object{confirmed: true, age: time.Hour})

	store := &recordingRemover{}
	res, err := newTestSweeper(db, store).RunOnce(ctx)
	require.NoError(t, err, "a RESTRICT refusal is a skip, not a failed pass")
	require.GreaterOrEqual(t, res.Skipped, 1)

	require.True(t, objectExists(t, db, held), "an object another layer references must survive")
	require.False(t, store.removed(heldKey), "the blob of a referenced object must stay")
	require.False(t, objectExists(t, db, free), "the pass must go on to the next unreferenced object")
	require.True(t, store.removed(freeKey))
}

// TestSweeperForeignRestrictSkipsReservation: an abandoned reservation
// the sweeper would reclaim with its own attachment rows, except that a
// foreign layer also holds it. The object stays, and so does the
// attachment row the sweeper deleted on the way: its delete has to roll
// back with the object's, or the count is left above the rows it counts.
func TestSweeperForeignRestrictSkipsReservation(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	wsID, userID := seedWorkspace(t, db)
	foreign := createForeignTable(t, db)

	held, heldKey := plantObject(t, db, wsID, object{refCount: 2, age: 3 * time.Hour})
	plantTaskAttachment(t, db, wsID, userID, held)
	plantForeignRow(t, db, foreign, held)
	free, freeKey := plantObject(t, db, wsID, object{refCount: 1, age: 2 * time.Hour})
	plantTaskAttachment(t, db, wsID, userID, free)

	store := &recordingRemover{}
	sweeper := newTestSweeper(db, store)
	sweeper.ReservationTTL = time.Hour
	res, err := sweeper.RunOnce(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, res.Skipped, 1)

	require.True(t, objectExists(t, db, held))
	require.Equal(t, 1, taskAttachmentCount(t, db, held),
		"the sweeper's own referrer must come back when the object cannot go")
	require.False(t, store.removed(heldKey))

	require.False(t, objectExists(t, db, free), "an abandoned reservation nobody else holds is reclaimed")
	require.Zero(t, taskAttachmentCount(t, db, free))
	require.True(t, store.removed(freeKey))
}

// TestSweeperForeignRestrictDoesNotStarveLaterRows: a whole batch of
// objects another layer holds sits ahead of a reclaimable one. They are
// skipped on every pass, so a sweep that re-read the head would never
// reach the row behind them; paging past them must.
func TestSweeperForeignRestrictDoesNotStarveLaterRows(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	wsID, _ := seedWorkspace(t, db)
	foreign := createForeignTable(t, db)

	const batch = 3
	held := make([]uint32, 0, batch)
	for range batch {
		id, _ := plantObject(t, db, wsID, object{confirmed: true, age: 2 * time.Hour})
		plantForeignRow(t, db, foreign, id)
		held = append(held, id)
	}
	free, freeKey := plantObject(t, db, wsID, object{confirmed: true, age: time.Hour})

	store := &recordingRemover{}
	sweeper := newTestSweeper(db, store)
	sweeper.BatchSize = batch
	// Bounded rather than one: residue an earlier test in this package
	// left reclaimable may take part of the first batch.
	for pass := 0; pass < 3 && objectExists(t, db, free); pass++ {
		_, err := sweeper.RunOnce(ctx)
		require.NoError(t, err)
	}

	require.False(t, objectExists(t, db, free), "a batch of held objects must not keep the sweep from the rows behind it")
	require.True(t, store.removed(freeKey))
	for _, id := range held {
		require.True(t, objectExists(t, db, id))
	}
}

// TestSweeperUnreferencedSweepSkipsReservations: an unconfirmed row at
// ref_count = 0 is the shape of another product's reservation before its
// upload lands. Only age reclaims a reservation, so the unreferenced sweep
// leaves it, and the unconfirmed sweep takes it once it is past the TTL.
func TestSweeperUnreferencedSweepSkipsReservations(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	wsID, _ := seedWorkspace(t, db)

	id, key := plantObject(t, db, wsID, object{})

	store := &recordingRemover{}
	_, err := newTestSweeper(db, store).RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, objectExists(t, db, id), "a reservation younger than the TTL is not reclaimed, whatever its count")
	require.False(t, store.removed(key))

	old := time.Now().UTC().Add(-storagegc.DefaultReservationTTL - time.Minute)
	_, err = db.ExecContext(ctx,
		`UPDATE storage_objects SET created_at = ?, updated_at = ? WHERE id = ?`, old, old, id)
	require.NoError(t, err)
	_, err = newTestSweeper(db, store).RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, objectExists(t, db, id), "a reservation past the TTL is abandoned")
	require.True(t, store.removed(key))
}

// TestSweeperBucketLocation: the first sweeper records its bucket; one
// configured for another bucket refuses every pass, so it never deletes
// rows while removing their keys from a bucket that does not hold them.
func TestSweeperBucketLocation(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()
	clearLocation := func() {
		_, err := db.ExecContext(context.Background(),
			`DELETE FROM instance_settings WHERE setting_key = 'storage.location'`)
		require.NoError(t, err)
	}
	clearLocation()
	t.Cleanup(clearLocation)

	here := storagegc.Location("storage:9000", "nodate")
	elsewhere := storagegc.Location("other-storage:9000", "nodate")

	_, err := newTestSweeperAt(db, &recordingRemover{}, here).RunOnce(ctx)
	require.NoError(t, err, "the first sweeper against a database records its bucket")
	var recorded string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT setting_value FROM instance_settings WHERE setting_key = 'storage.location'`).Scan(&recorded))
	require.Equal(t, here, recorded)

	wsID, _ := seedWorkspace(t, db)
	id, key := plantObject(t, db, wsID, object{confirmed: true})

	wrong := &recordingRemover{}
	_, err = newTestSweeperAt(db, wrong, elsewhere).RunOnce(ctx)
	require.ErrorIs(t, err, storagegc.ErrStorageLocationMismatch)
	require.True(t, objectExists(t, db, id), "a sweeper on another bucket must not reclaim anything")
	require.False(t, wrong.removed(key))

	right := &recordingRemover{}
	_, err = newTestSweeperAt(db, right, here).RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, objectExists(t, db, id), "the matching sweeper reclaims the same object")
	require.True(t, right.removed(key))
}

func newTestSweeperAt(db *sql.DB, store storagegc.ObjectRemover, location string) *storagegc.Sweeper {
	s := newTestSweeper(db, store)
	s.Location = location
	return s
}
