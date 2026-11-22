package attachkit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "attachkit_test",
})

// fakeStore answers StatObject from a set of keys the test says exist.
type fakeStore map[string]bool

func (f fakeStore) StatObject(_ context.Context, key string) (int64, error) {
	if f[key] {
		return 1, nil
	}
	return 0, errors.New("not found")
}

type fixtures struct {
	wsID, userID, taskID uint32
	wsPub, taskPub       dbtype.PublicID
}

func seed(ctx context.Context, t *testing.T, db *sql.DB) fixtures {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slugPart := suffix[len(suffix)-10:]
	insert := func(q string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}
	wsPub, taskPub := dbtype.New(), dbtype.New()
	wsID := insert(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "ws-"+slugPart, "AttachKit Test "+suffix)
	userID := insert(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "attachkit+"+suffix+"@example.test", "AttachKit Tester")
	insert(`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at) VALUES (?, ?, ?, 'owner', NOW())`,
		dbtype.New(), wsID, userID)
	projectID := insert(`INSERT INTO projects (public_id, workspace_id, name, slug, identifier) VALUES (?, ?, ?, ?, ?)`,
		dbtype.New(), wsID, "AttachKit Project", "pj-"+slugPart, "AKT")
	taskID := insert(`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility) VALUES (?, ?, ?, 1, 'Test task', 'public')`,
		taskPub, wsID, projectID)
	return fixtures{wsID: wsID, userID: userID, taskID: taskID, wsPub: wsPub, taskPub: taskPub}
}

func purge(t *testing.T, db *sql.DB, wsID uint32) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Logf("purge: acquire conn: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	for _, q := range []string{
		`DELETE FROM events WHERE workspace_id = ?`,
		`DELETE FROM attachments WHERE workspace_id = ?`,
		`DELETE FROM storage_objects WHERE workspace_id = ?`,
		`DELETE FROM tasks WHERE workspace_id = ?`,
		`DELETE FROM projects WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := conn.ExecContext(ctx, q, wsID); err != nil {
			t.Logf("purge %q: %v", q, err)
		}
	}
	_, _ = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
}

func start(t *testing.T) (context.Context, *sql.DB, fixtures) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	inst, err := shared.Start(ctx)
	require.NoError(t, err)
	fx := seed(ctx, t, inst.DB)
	t.Cleanup(func() { purge(t, inst.DB, fx.wsID) })
	return ctx, inst.DB, fx
}

func args(fx fixtures, body string, store ObjectStatter) Args {
	sum := sha256.Sum256([]byte(body))
	return Args{
		WorkspaceID:       fx.wsID,
		WorkspacePublicID: fx.wsPub,
		TaskID:            fx.taskID,
		TaskPublicID:      fx.taskPub,
		Filename:          "notes.txt",
		ContentType:       "text/plain",
		Size:              uint64(len(body)),
		SHA256:            sum[:],
		UploaderUserID:    fx.userID,
		Actor:             mutationlog.Actor{UserID: fx.userID, WorkspaceID: fx.wsID},
		Store:             store,
	}
}

func TestAttachKitReserve(t *testing.T) {
	ctx, db, fx := start(t)
	store := fakeStore{}

	eventCount := func() int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'task.attachment.added'`, fx.wsID).Scan(&n))
		return n
	}
	refCount := func(id uint32) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT ref_count FROM storage_objects WHERE id = ?`, id).Scan(&n))
		return n
	}

	a := args(fx, "first body", store)
	a.SourceUID = "asana:1001"
	first, err := Reserve(ctx, db, a)
	require.NoError(t, err)
	assert.False(t, first.Deduplicated)
	assert.False(t, first.AlreadyUploaded)
	assert.NotZero(t, first.AttachmentID)
	assert.Equal(t, storage.KeyForWorkspace(hex.EncodeToString(fx.wsPub[:]), hex.EncodeToString(a.SHA256)), first.StorageKey)
	assert.Equal(t, 1, refCount(first.StorageObjectID))
	assert.Equal(t, 1, eventCount())

	var sourceUID sql.NullString
	var uploaderID, objectID uint32
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT source_uid, uploader_id, storage_object_id FROM attachments WHERE id = ?`, first.AttachmentID).
		Scan(&sourceUID, &uploaderID, &objectID))
	assert.Equal(t, "asana:1001", sourceUID.String)
	assert.Equal(t, fx.userID, uploaderID)
	assert.Equal(t, first.StorageObjectID, objectID)

	t.Run("dedup onto an unconfirmed reservation still uploads", func(t *testing.T) {
		second, err := Reserve(ctx, db, args(fx, "first body", store))
		require.NoError(t, err)
		assert.True(t, second.Deduplicated)
		assert.False(t, second.AlreadyUploaded)
		assert.Equal(t, first.StorageObjectID, second.StorageObjectID)
		assert.Equal(t, first.StorageKey, second.StorageKey)
		assert.Equal(t, 2, refCount(first.StorageObjectID))

		var stored sql.NullString
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT source_uid FROM attachments WHERE id = ?`, second.AttachmentID).Scan(&stored))
		assert.False(t, stored.Valid, "an empty SourceUID stores NULL")
	})

	t.Run("dedup onto a confirmed object depends on the store", func(t *testing.T) {
		require.NoError(t, MarkUploaded(ctx, db, MarkArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, StorageObjectID: first.StorageObjectID, Size: 10,
		}))

		missing, err := Reserve(ctx, db, args(fx, "first body", store))
		require.NoError(t, err)
		assert.True(t, missing.Deduplicated)
		assert.False(t, missing.AlreadyUploaded, "a confirmed row whose object is gone is uploaded again")

		store[first.StorageKey] = true
		present, err := Reserve(ctx, db, args(fx, "first body", store))
		require.NoError(t, err)
		assert.True(t, present.Deduplicated)
		assert.True(t, present.AlreadyUploaded)
	})

	t.Run("a task that no longer resolves writes nothing", func(t *testing.T) {
		before := eventCount()
		gone := args(fx, "other body", store)
		gone.TaskID = fx.taskID + 1_000_000
		_, err := Reserve(ctx, db, gone)
		assert.ErrorIs(t, err, ErrTaskGone)
		assert.Equal(t, before, eventCount())

		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM storage_objects WHERE workspace_id = ? AND sha256 = ?`, fx.wsID, gone.SHA256).Scan(&n))
		assert.Zero(t, n)
	})
}

func TestAttachKitMarkUploaded(t *testing.T) {
	ctx, db, fx := start(t)

	read := func(id uint32) (sql.NullTime, uint64) {
		var at sql.NullTime
		var size uint64
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT uploaded_at, byte_size FROM storage_objects WHERE id = ?`, id).Scan(&at, &size))
		return at, size
	}

	res, err := Reserve(ctx, db, args(fx, "mark body", fakeStore{}))
	require.NoError(t, err)
	at, _ := read(res.StorageObjectID)
	require.False(t, at.Valid, "a reservation starts unconfirmed")

	t.Run("a task that no longer resolves stamps nothing", func(t *testing.T) {
		err := MarkUploaded(ctx, db, MarkArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID + 1_000_000, StorageObjectID: res.StorageObjectID, Size: 42,
		})
		assert.ErrorIs(t, err, ErrTaskGone)
		at, size := read(res.StorageObjectID)
		assert.False(t, at.Valid)
		assert.Equal(t, uint64(len("mark body")), size)
	})

	t.Run("stamps the measured size", func(t *testing.T) {
		require.NoError(t, MarkUploaded(ctx, db, MarkArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, StorageObjectID: res.StorageObjectID, Size: 42,
		}))
		at, size := read(res.StorageObjectID)
		assert.True(t, at.Valid)
		assert.Equal(t, uint64(42), size)
	})

	t.Run("without a task it locks nothing and still stamps", func(t *testing.T) {
		require.NoError(t, MarkUploaded(ctx, db, MarkArgs{
			WorkspaceID: fx.wsID, StorageObjectID: res.StorageObjectID, Size: 7,
		}))
		_, size := read(res.StorageObjectID)
		assert.Equal(t, uint64(7), size)
	})

	var events int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'task.attachment.added'`, fx.wsID).Scan(&events))
	assert.Equal(t, 1, events, "confirming appends no event of its own")
}
