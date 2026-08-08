package teardown

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// teardownDB lazily boots a shared MySQL testcontainer with the full
// repo schema. The delete pipelines need a real server: the regression
// they guard is about what survives when an InnoDB transaction fails
// mid-flight, which no fake driver reproduces faithfully.
var teardownDB = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{Database: "nodate_auth_teardown_test"})

// requireTeardownDB skips when integration tests are not enabled and
// otherwise returns the shared instance.
func requireTeardownDB(t *testing.T) *testhelpers.MySQLInstance {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping teardown integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run teardown integration tests")
	}
	inst, err := teardownDB.Start(context.Background())
	require.NoError(t, err, "start mysql testcontainer")
	return inst
}

// recordingSweeper is a [BlobSweeper] that remembers what it was asked
// to delete instead of talking to MinIO. Object storage has no undo, so
// "was RemoveObjects called at all" is the assertion that matters.
type recordingSweeper struct {
	mu      sync.Mutex
	calls   int
	removed []string
	err     error
}

func (s *recordingSweeper) RemoveObjects(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.removed = append(s.removed, keys...)
	return s.err
}

func (s *recordingSweeper) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([]string(nil), s.removed...)
}

// impatientDB opens a second pool against the same server, pinned to a
// single connection whose innodb_lock_wait_timeout is 1 second. The
// pipeline under test runs on this pool so a row lock held elsewhere
// fails its transaction in about a second instead of the default 50.
func impatientDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// One connection, never recycled, so the session setting below
	// applies to every statement the pipeline issues.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	_, err = db.Exec("SET SESSION innodb_lock_wait_timeout = 1")
	require.NoError(t, err)
	var got int
	require.NoError(t, db.QueryRow("SELECT @@innodb_lock_wait_timeout").Scan(&got))
	require.Equal(t, 1, got, "lock wait timeout must stick to the pooled connection")
	return db
}

// seedWorkspaceWithBlob creates a workspace plus one workspace-scoped
// storage_objects row and returns the workspace internal id and the
// blob's storage key, alongside the workspace public id the teardown
// pipeline logs under.
func seedWorkspaceWithBlob(t *testing.T, db *sql.DB, slugPrefix string) (uint32, uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	q := generated.New(db)

	pub := types.New()
	wsID64, err := q.CreateWorkspace(ctx, generated.CreateWorkspaceParams{
		PublicID: pub,
		Slug:     slugPrefix + "-" + pub.String(),
		Name:     "Teardown Test Workspace",
		Timezone: "UTC",
		Country:  sql.NullString{String: "US", Valid: true},
	})
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- workspaces.id is INT UNSIGNED, fits uint32

	digest := sha256.Sum256([]byte(pub.String()))
	key := "workspace/" + pub.String() + "/blob"
	_, err = db.ExecContext(ctx, `
		INSERT INTO storage_objects
			(public_id, workspace_id, sha256, byte_size, content_type, storage_key, ref_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		types.New(), wsID, digest[:], 11, "text/plain", key, 0)
	require.NoError(t, err)

	return wsID, pub.UUID(), key
}

func workspaceExists(t *testing.T, db *sql.DB, wsID uint32) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM workspaces WHERE id = ?", wsID).Scan(&n))
	return n > 0
}

func storageObjectCount(t *testing.T, db *sql.DB, wsID uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM storage_objects WHERE workspace_id = ?", wsID).Scan(&n))
	return n
}

// TestWorkspace_FailedTransactionLeavesBlobsIntact is the regression
// that matters: when the delete transaction cannot complete, the blobs
// must still be there. A row lock held by another session makes the
// hard DELETE time out, which is the everyday version of the failure
// (deadlock, lock wait, cancelled request).
//
// Sweeping object storage before the transaction inverts the outcome
// into the one failure the system cannot repair: every row stays alive
// and fully consistent while the bytes are gone, so the UI keeps
// listing attachments whose downloads all 404.
func TestWorkspace_FailedTransactionLeavesBlobsIntact(t *testing.T) {
	inst := requireTeardownDB(t)
	ctx := context.Background()

	wsID, wsPub, key := seedWorkspaceWithBlob(t, inst.DB, "teardown-fail")

	// Hold an exclusive lock on the workspaces row from an unrelated
	// session. The pipeline's DELETE will queue behind it.
	lockTx, err := inst.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback() }()
	var lockedID uint32
	require.NoError(t, lockTx.QueryRowContext(ctx,
		"SELECT id FROM workspaces WHERE id = ? FOR UPDATE", wsID).Scan(&lockedID))

	db := impatientDB(t, inst.DSN)
	sweeper := &recordingSweeper{}

	res, err := Workspace(ctx, db, generated.New(db), sweeper, wsID, wsPub)
	require.Error(t, err, "the delete transaction must fail while the row is locked")
	assert.False(t, res.Deleted)

	calls, removed := sweeper.snapshot()
	assert.Zero(t, calls, "object storage must not be touched when the transaction fails")
	assert.Empty(t, removed)

	// Release the lock and confirm the database is untouched too, so the
	// blob and the rows that name it are still a matching pair.
	require.NoError(t, lockTx.Rollback())
	assert.True(t, workspaceExists(t, inst.DB, wsID), "workspace must survive a failed delete")
	assert.Equal(t, 1, storageObjectCount(t, inst.DB, wsID), "storage_objects row must survive a failed delete")
	var keyStillThere int
	require.NoError(t, inst.DB.QueryRow(
		"SELECT COUNT(*) FROM storage_objects WHERE storage_key = ?", key).Scan(&keyStillThere))
	assert.Equal(t, 1, keyStillThere)
}

// TestWorkspace_CommittedDeleteSweepsBlobs pins the other half: once the
// transaction commits, the blobs really are swept, and the keys the
// sweeper receives are the ones the deleted rows named.
func TestWorkspace_CommittedDeleteSweepsBlobs(t *testing.T) {
	inst := requireTeardownDB(t)
	ctx := context.Background()

	wsID, wsPub, key := seedWorkspaceWithBlob(t, inst.DB, "teardown-ok")
	sweeper := &recordingSweeper{}

	res, err := Workspace(ctx, inst.DB, generated.New(inst.DB), sweeper, wsID, wsPub)
	require.NoError(t, err)
	assert.True(t, res.Deleted)
	assert.Equal(t, int64(1), res.StorageObjectsDeleted)
	assert.Equal(t, int64(0), res.MinioErrors)

	calls, removed := sweeper.snapshot()
	assert.Equal(t, 1, calls)
	assert.Equal(t, []string{key}, removed)

	assert.False(t, workspaceExists(t, inst.DB, wsID))
	assert.Zero(t, storageObjectCount(t, inst.DB, wsID))
}

// TestWorkspace_SweepFailureIsReportedNotFatal covers the direction the
// new ordering can fail in: the rows are gone, MinIO refuses, and the
// caller is told so (MinioErrors=1) rather than the delete being undone.
// The keys are written to the log at that point so the orphans stay
// reclaimable.
func TestWorkspace_SweepFailureIsReportedNotFatal(t *testing.T) {
	inst := requireTeardownDB(t)
	ctx := context.Background()

	wsID, wsPub, _ := seedWorkspaceWithBlob(t, inst.DB, "teardown-sweep-err")
	sweeper := &recordingSweeper{err: assert.AnError}

	res, err := Workspace(ctx, inst.DB, generated.New(inst.DB), sweeper, wsID, wsPub)
	require.NoError(t, err, "a storage failure must not fail the delete after commit")
	assert.True(t, res.Deleted)
	assert.Equal(t, int64(1), res.MinioErrors)
	assert.False(t, workspaceExists(t, inst.DB, wsID))
}

// TestWorkspace_NilStorageClientIsSkipped guards the optional-storage
// contract: NF_S3_ENDPOINT may be unset, and the handlers hand the
// pipeline a nil *storage.Client. A nil pointer in an interface is not
// itself nil, so this is exactly where a sweep would panic.
func TestWorkspace_NilStorageClientIsSkipped(t *testing.T) {
	inst := requireTeardownDB(t)
	ctx := context.Background()

	wsID, wsPub, _ := seedWorkspaceWithBlob(t, inst.DB, "teardown-nil-store")

	var nilClient *storage.Client
	res, err := Workspace(ctx, inst.DB, generated.New(inst.DB), nilClient, wsID, wsPub)
	require.NoError(t, err)
	assert.True(t, res.Deleted)
	assert.Equal(t, int64(0), res.MinioErrors)
	assert.False(t, workspaceExists(t, inst.DB, wsID))

	// And with no sweeper at all.
	wsID2, wsPub2, _ := seedWorkspaceWithBlob(t, inst.DB, "teardown-no-store")
	res, err = Workspace(ctx, inst.DB, generated.New(inst.DB), nil, wsID2, wsPub2)
	require.NoError(t, err)
	assert.True(t, res.Deleted)
}
