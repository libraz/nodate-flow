package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// uploadAvatar drives a real multipart/form-data POST against
// /me/avatar (served by the auth-api router, mounted via the composite
// test handler). Returns the parsed user envelope including the rendered
// avatarUrl, plus the raw status code so error-path tests can assert
// non-2xx outcomes without surrendering the body.
func uploadAvatar(t *testing.T, token, filename, contentType string, payload []byte) (avatarUploadBody, int) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	hdr.Set("Content-Type", contentType)
	part, err := writer.CreatePart(hdr)
	require.NoError(t, err, "create multipart part")
	_, err = io.Copy(part, bytes.NewReader(payload))
	require.NoError(t, err, "copy payload into multipart part")
	require.NoError(t, writer.Close(), "finalise multipart writer")

	req, err := http.NewRequest(http.MethodPost, testServerURL+"/me/avatar", body)
	require.NoError(t, err, "build POST /me/avatar request")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "POST /me/avatar")
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var out avatarUploadBody
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), "decode /me/avatar body=%s", string(raw))
	}
	return out, resp.StatusCode
}

type avatarUploadBody struct {
	ID        string  `json:"id"`
	AvatarURL *string `json:"avatarUrl"`
}

// sha256Of returns the lowercase hex sha256 of the given bytes. Kept
// in this file (rather than reusing the helper from
// attachment_dedup_test.go) so the avatar tests stay self-contained.
func sha256Of(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:])
}

// userInternalIdAndAvatar returns (id, avatar_storage_object_id, has_avatar)
// for a user identified by public id (UUID v7 textual form).
func userInternalIdAndAvatar(t *testing.T, db *sql.DB, userPublicID string) (uint32, sql.NullInt32) {
	t.Helper()
	var (
		id     uint32
		avatar sql.NullInt32
	)
	err := db.QueryRowContext(context.Background(),
		`SELECT id, avatar_storage_object_id
		   FROM users
		  WHERE public_id = UUID_TO_BIN(?, 0)
		  LIMIT 1`, userPublicID).Scan(&id, &avatar)
	require.NoError(t, err)
	return id, avatar
}

// countAvatarStorageObjectsForUser returns the number of storage_objects
// rows owned by the supplied internal user id. Used to assert dedup
// (count stays at 1) and replace (count stays at 1 but the row id
// changes).
func countAvatarStorageObjectsForUser(t *testing.T, db *sql.DB, userID uint32) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM storage_objects WHERE owner_user_id = ?`, userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// storageKeyByObjectID resolves a storage_objects row by internal id and
// returns its storage_key. Used by the replace test to capture the key
// of the soon-to-be-deleted previous avatar.
func storageKeyByObjectID(t *testing.T, db *sql.DB, id uint32) string {
	t.Helper()
	var key string
	err := db.QueryRowContext(context.Background(),
		`SELECT storage_key FROM storage_objects WHERE id = ?`, id).Scan(&key)
	require.NoError(t, err)
	return key
}

// TestAvatarDedup verifies that uploading the same image twice for one
// user reuses a single storage_objects row (ref_count semantics) and
// stores exactly one MinIO object.
func TestAvatarDedup(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	payload := makePNG(t, 16, 16, color.RGBA{R: 100, G: 200, B: 50, A: 255})

	// First upload: must allocate a fresh storage_objects row + write
	// the bytes to MinIO.
	body1, status1 := uploadAvatar(t, tt.AccessToken, "avatar.png", "image/png", payload)
	require.Equal(t, http.StatusOK, status1, "first avatar upload must succeed")
	require.NotNil(t, body1.AvatarURL, "first avatar upload must populate avatarUrl")
	require.NotEmpty(t, *body1.AvatarURL)

	uid, avatar1 := userInternalIdAndAvatar(t, testDB, tt.UserPublicID)
	require.True(t, avatar1.Valid, "users.avatar_storage_object_id must be set after first upload")
	require.Equal(t, 1, countAvatarStorageObjectsForUser(t, testDB, uid))
	key1 := storageKeyByObjectID(t, testDB, uint32(avatar1.Int32))
	testStorage.MustExist(t, key1)

	// Second upload: same bytes -> dedup hit. The DB row should NOT
	// change id (ref_count goes 1 -> 2 -> 1 once the previous link is
	// dropped, ending net 1 because the previous and the new are the
	// same row and the handler skips the dec when prev == new).
	body2, status2 := uploadAvatar(t, tt.AccessToken, "avatar-again.png", "image/png", payload)
	require.Equal(t, http.StatusOK, status2)
	require.NotNil(t, body2.AvatarURL)

	_, avatar2 := userInternalIdAndAvatar(t, testDB, tt.UserPublicID)
	require.True(t, avatar2.Valid)
	require.Equal(t, avatar1.Int32, avatar2.Int32,
		"dedup must keep the same storage_objects row")
	require.Equal(t, 1, countAvatarStorageObjectsForUser(t, testDB, uid),
		"dedup must keep exactly one storage_objects row for the user")
	testStorage.MustExist(t, key1)
}

// TestAvatarReplaceCleansOld verifies that uploading a different image
// drops the previous storage_objects ref_count to 0 and physically
// removes the old blob from MinIO; only the new blob remains.
func TestAvatarReplaceCleansOld(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	first := makePNG(t, 16, 16, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	second := makePNG(t, 16, 16, color.RGBA{R: 250, G: 240, B: 230, A: 255})
	require.NotEqual(t, sha256Of(first), sha256Of(second),
		"the two payloads must produce different sha256 so dedup does not hit")

	body1, status1 := uploadAvatar(t, tt.AccessToken, "first.png", "image/png", first)
	require.Equal(t, http.StatusOK, status1)
	require.NotNil(t, body1.AvatarURL)

	uid, avatar1 := userInternalIdAndAvatar(t, testDB, tt.UserPublicID)
	require.True(t, avatar1.Valid)
	prevID := uint32(avatar1.Int32)
	prevKey := storageKeyByObjectID(t, testDB, prevID)
	testStorage.MustExist(t, prevKey)

	body2, status2 := uploadAvatar(t, tt.AccessToken, "second.png", "image/png", second)
	require.Equal(t, http.StatusOK, status2)
	require.NotNil(t, body2.AvatarURL)
	require.NotEqual(t, *body1.AvatarURL, *body2.AvatarURL,
		"replacing the avatar must change the proxy URL (cache-buster suffix derives from new id)")

	_, avatar2 := userInternalIdAndAvatar(t, testDB, tt.UserPublicID)
	require.True(t, avatar2.Valid)
	require.NotEqual(t, avatar1.Int32, avatar2.Int32,
		"replace must point users.avatar_storage_object_id at a new row")
	require.Equal(t, 1, countAvatarStorageObjectsForUser(t, testDB, uid),
		"replace must leave exactly one storage_objects row owned by the user")

	// The previous storage_objects row must be GC'd, and so must the
	// underlying MinIO blob.
	var leftover int
	err := testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM storage_objects WHERE id = ?`, prevID).Scan(&leftover)
	require.NoError(t, err)
	require.Equal(t, 0, leftover, "previous storage_objects row must be deleted on replace")
	testStorage.MustNotExist(t, prevKey)
}

// TestAvatarDifferentUsers verifies that owner-scoped dedup does NOT
// merge avatars across users: two different users uploading the same
// bytes produce two distinct storage_objects rows and two distinct
// MinIO blobs (under different content-addressed keys).
func TestAvatarDifferentUsers(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	a := newTenant(t)
	b := newTenant(t)
	require.NotEqual(t, a.UserPublicID, b.UserPublicID, "tenants must own distinct users")

	payload := makePNG(t, 16, 16, color.RGBA{R: 77, G: 77, B: 77, A: 255})

	bodyA, statusA := uploadAvatar(t, a.AccessToken, "a.png", "image/png", payload)
	require.Equal(t, http.StatusOK, statusA)
	require.NotNil(t, bodyA.AvatarURL)
	bodyB, statusB := uploadAvatar(t, b.AccessToken, "b.png", "image/png", payload)
	require.Equal(t, http.StatusOK, statusB)
	require.NotNil(t, bodyB.AvatarURL)

	uidA, avatarA := userInternalIdAndAvatar(t, testDB, a.UserPublicID)
	uidB, avatarB := userInternalIdAndAvatar(t, testDB, b.UserPublicID)
	require.True(t, avatarA.Valid && avatarB.Valid)
	require.NotEqual(t, avatarA.Int32, avatarB.Int32,
		"per-user scoped dedup must produce distinct storage_objects rows across users")

	keyA := storageKeyByObjectID(t, testDB, uint32(avatarA.Int32))
	keyB := storageKeyByObjectID(t, testDB, uint32(avatarB.Int32))
	require.NotEqual(t, keyA, keyB,
		"distinct user-scoped storage keys (path includes user public id hex)")
	require.True(t, strings.HasPrefix(keyA, "user/"))
	require.True(t, strings.HasPrefix(keyB, "user/"))
	testStorage.MustExist(t, keyA)
	testStorage.MustExist(t, keyB)
	require.Equal(t, 1, countAvatarStorageObjectsForUser(t, testDB, uidA))
	require.Equal(t, 1, countAvatarStorageObjectsForUser(t, testDB, uidB))
}
