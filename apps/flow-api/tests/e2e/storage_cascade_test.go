// Storage cleanup + avatar-proxy edge-case suite.
//
// Historically the schema-level CASCADE rules
//
//   - workspaces.id → storage_objects.workspace_id   (CASCADE)
//   - users.id      → storage_objects.owner_user_id  (CASCADE)
//   - storage_objects.id → users.avatar_storage_object_id (SET NULL)
//
// were the only thing keeping storage_objects rows from leaking when
// their parent went away — and the underlying MinIO blobs were left
// behind for the GC sweeper because a raw row DELETE bypasses every
// application-layer cleanup path.
//
// The admin immediate-delete endpoints (DELETE /admin/workspaces/{id}
// and DELETE /admin/users/{id}, both with {"confirm": true}) close that
// gap: they enumerate storage_objects BEFORE the CASCADE-anchored
// DELETE fires, sweep MinIO in bulk, and only then issue the hard
// DELETE. These tests exercise the new contract end-to-end, asserting
// that BOTH the DB rows AND the MinIO blobs disappear in a single API
// call.
//
// The avatar-proxy test guards against URL-confusion: the proxy must
// return 404 for users whose avatar_url column is set to an external
// HTTPS URL with no uploaded blob behind it, never reach out to that
// URL, and never serve placeholder bytes from the wrong identity.
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"image/color"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// internalWorkspaceID resolves a workspace public id (UUID v7 textual
// form) to its internal autoincrement id. Test-only direct SQL: the
// API never exposes the internal id.
func internalWorkspaceID(t *testing.T, db *sql.DB, workspacePublicID string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(context.Background(),
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID).Scan(&id)
	require.NoError(t, err)
	return id
}

// countStorageObjectsForWorkspace returns how many storage_objects
// rows are scoped to the supplied internal workspace id. Used to
// verify the workspace-purge path leaves the table clean.
func countStorageObjectsForWorkspace(t *testing.T, db *sql.DB, wsID uint32) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM storage_objects WHERE workspace_id = ?`, wsID).Scan(&n)
	require.NoError(t, err)
	return n
}

// countStorageObjectsForUser returns how many storage_objects rows
// are owned by the supplied internal user id. Used to verify the
// user-purge path removes the avatar row.
func countStorageObjectsForUser(t *testing.T, db *sql.DB, userID uint32) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM storage_objects WHERE owner_user_id = ?`, userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// internalUserID resolves a user public id to its internal id.
func internalUserID(t *testing.T, db *sql.DB, userPublicID string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(context.Background(),
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		userPublicID).Scan(&id)
	require.NoError(t, err)
	return id
}

// TestWorkspaceDeleteRemovesStorageRowsAndBlobs is the storage-cascade
// regression originally aimed at fk_storage_objects_workspace, now
// upgraded to drive the same scenario through the admin immediate-delete
// endpoint so we additionally assert that the underlying MinIO blob is
// removed.
//
// Before the delete endpoint existed this case was only able to assert
// the MySQL CASCADE; the MinIO side was a known leak. The contract is
// now: a single admin call wipes both the storage_objects rows AND the
// physical blobs, leaving nothing for a downstream sweeper to mop up.
func TestWorkspaceDeleteRemovesStorageRowsAndBlobs(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "ws cascade")

	payload := makePNG(t, 8, 8, color.RGBA{R: 50, G: 100, B: 150, A: 255})
	res := presignAttachment(t, tt.AccessToken, taskID, "blob.png", "image/png", payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	require.Equalf(t, 1, countStorageObjectsForWorkspace(t, testDB, wsID),
		"baseline: workspace must own exactly one storage_objects row")
	testStorage.MustExist(t, res.StorageKey)

	// Promote a fresh user to instance admin so the delete call is
	// authorised; the workspace owner has tenant scope only.
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	// Single-step destructive delete by the instance admin. There is no
	// soft-disable precondition.
	out := adminDeleteWorkspace(t, admin.AccessToken, tt.WorkspacePublicID)
	require.Truef(t, out.Deleted, "delete must report deleted=true; got %+v", out)
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(1),
		"delete must report at least the one attachment storage object; got %d", out.StorageObjectsDeleted)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO must produce zero errors; got %d", out.MinioErrors)

	require.Equalf(t, 0, countStorageObjectsForWorkspace(t, testDB, wsID),
		"workspace delete must remove every storage_objects row scoped to the workspace")
	testStorage.MustNotExist(t, res.StorageKey)

	// The workspace row itself MUST be gone after delete; the new
	// contract is a single-step immediate hard delete.
	var wsCount int
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM workspaces WHERE id = ?`, wsID).Scan(&wsCount))
	require.Equalf(t, 0, wsCount, "delete must hard-delete the workspaces row")
}

// TestUserDeleteRemovesAvatarRowAndBlob is the user-side counterpart of
// TestWorkspaceDeleteRemovesStorageRowsAndBlobs: the admin delete
// endpoint must remove both the avatar storage_objects row AND the
// underlying MinIO blob in a single call. Before the endpoint landed,
// the FK CASCADE only cleaned the row and left the blob to the GC
// sweeper.
func TestUserDeleteRemovesAvatarRowAndBlob(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	target := newTenant(t)
	payload := makePNG(t, 16, 16, color.RGBA{R: 30, G: 60, B: 90, A: 255})

	body, status := uploadAvatar(t, target.AccessToken, "avatar.png", "image/png", payload)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, body.AvatarURL)

	uid := internalUserID(t, testDB, target.UserPublicID)
	_, avatar := userInternalIDAndAvatar(t, testDB, target.UserPublicID)
	require.True(t, avatar.Valid, "baseline: avatar must be set")
	avatarKey := storageKeyByObjectID(t, testDB, avatar.Int32)
	require.Equalf(t, 1, countStorageObjectsForUser(t, testDB, uid),
		"baseline: user must own exactly one storage_objects row")
	testStorage.MustExist(t, avatarKey)

	// Single-step admin delete, no soft-disable precondition.
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	out := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.True(t, out.Deleted, "user delete response must report deleted=true")
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(1),
		"user delete must report at least the avatar storage object; got %d", out.StorageObjectsDeleted)
	require.Equal(t, int64(0), out.MinioErrors)

	require.Equalf(t, 0, countStorageObjectsForUser(t, testDB, uid),
		"user delete must remove the avatar storage_objects row")
	testStorage.MustNotExist(t, avatarKey)

	var userCount int
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = ?`, uid).Scan(&userCount))
	require.Equalf(t, 0, userCount, "user delete must hard-delete the users row")
}

// TestAvatarProxyExternalURLReturns404 verifies that the avatar
// proxy is the one and only path through which uploaded avatars are
// served — it MUST NOT proxy to externally-hosted avatar_url values.
//
// The flow under test is:
//
//  1. Register a user (no avatar uploaded).
//  2. Set users.avatar_url to a https:// URL via direct SQL — this
//     mirrors the OIDC callback path that records an external avatar
//     without uploading bytes to our MinIO.
//  3. GET /avatars/{userId} — the proxy must return 404 because
//     avatar_storage_object_id IS NULL, and MUST NOT fetch the
//     external URL on the user's behalf.
func TestAvatarProxyExternalURLReturns404(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	uid := internalUserID(t, testDB, tt.UserPublicID)

	// Verify the precondition: no avatar uploaded yet, so
	// avatar_storage_object_id IS NULL.
	var soID sql.NullInt32
	err := testDB.QueryRowContext(context.Background(),
		`SELECT avatar_storage_object_id FROM users WHERE id = ?`, uid).Scan(&soID)
	require.NoError(t, err)
	require.Falsef(t, soID.Valid,
		"precondition: fresh user must have no avatar_storage_object_id; got %d", soID.Int32)

	// Plant an external URL directly. Test-only direct SQL: there is
	// no admin endpoint to set avatar_url to an arbitrary URL, and
	// the OIDC callback path requires a real provider session.
	_, err = testDB.ExecContext(context.Background(),
		`UPDATE users SET avatar_url = ? WHERE id = ?`,
		"https://example.com/external-avatar.jpg", uid)
	require.NoError(t, err, "set external avatar_url must succeed")

	// Hit the proxy. It MUST return 404 because the proxy path only
	// serves uploaded blobs (avatar_storage_object_id IS NOT NULL)
	// and never makes outbound requests. We use a dedicated client
	// without redirects so we can spot any rogue 302 to the external
	// URL.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := fmt.Sprintf("%s/avatars/%s", testServerURL, tt.UserPublicID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equalf(t, http.StatusNotFound, resp.StatusCode,
		"avatar proxy must 404 when only avatar_url (external) is set; got %d", resp.StatusCode)
	require.NotEqualf(t, "https://example.com/external-avatar.jpg",
		resp.Header.Get("Location"),
		"avatar proxy must NEVER redirect to the external avatar_url")
	require.Empty(t, resp.Header.Get("Location"),
		"avatar proxy must not surface a Location header at all")
}
