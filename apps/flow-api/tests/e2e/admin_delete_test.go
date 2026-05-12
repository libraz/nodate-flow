// E2E coverage for the instance-admin immediate destructive delete
// endpoints
//
//	DELETE /admin/workspaces/{wsId}
//	DELETE /admin/users/{userId}
//
// served by auth-api and reached through the composite test handler.
//
// Contract (single-step, no soft-disable precondition):
//
//   - Body MUST be {"confirm": true}; missing or false returns 400 with
//     WORKSPACE.DELETE.CONFIRM_REQUIRED / USER.DELETE.CONFIRM_REQUIRED.
//   - Self-delete on the user endpoint returns 400
//     USER.DELETE.SELF_NOT_ALLOWED (replaces the old overloaded
//     VALIDATION.PATH_PARAM.INVALID).
//   - Success → 200 {deleted, storageObjectsDeleted, minioErrors}.
//   - Workspace delete: every storage_objects row + MinIO blob owned by
//     the workspace is swept, then the workspaces row is hard-deleted via
//     CASCADE.
//   - User delete: orphaned avatar storage objects + uploader-owned
//     attachment blobs whose ref_count drops to zero are removed; shared
//     blobs (still referenced by another user) survive intact.
//   - Audit actions: admin.workspace.delete / admin.user.delete.
//   - Idempotent on missing target (sql.ErrNoRows from
//     AdminFindWorkspaceIdByPublicId / AdminFindUserIdByPublicId): 200
//     with deleted=false rather than 404.
//   - Non-admin caller: 403 (admin middleware rejects before the handler
//     runs).
package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// adminDeleteOutput mirrors the shared response envelope for both admin
// delete endpoints AND the workspace-owner self-delete endpoint. Same
// shape on success and on idempotent retry (the in-handler "already
// gone" path), which is why tests for both flows share this struct.
type adminDeleteOutput struct {
	Deleted               bool  `json:"deleted"`
	StorageObjectsDeleted int64 `json:"storageObjectsDeleted"`
	MinioErrors           int64 `json:"minioErrors"`
}

// adminDeleteWorkspace issues DELETE /admin/workspaces/{wsId} with
// confirm=true and asserts a 2xx, returning the parsed envelope. For
// negative-path tests use adminDeleteWorkspaceStatus.
func adminDeleteWorkspace(t *testing.T, token, wsPublicID string) adminDeleteOutput {
	t.Helper()
	var out adminDeleteOutput
	doJSON(t, http.MethodDelete,
		fmt.Sprintf("%s/admin/workspaces/%s", testServerURL, wsPublicID),
		token, map[string]any{"confirm": true}, &out)
	return out
}

// adminDeleteWorkspaceStatus is the negative-path counterpart to
// adminDeleteWorkspace: it returns the raw status + body so callers can
// assert specific failure codes / decode error envelopes.
func adminDeleteWorkspaceStatus(t *testing.T, token, wsPublicID string, body any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodDelete,
		fmt.Sprintf("%s/admin/workspaces/%s", testServerURL, wsPublicID),
		token, body)
}

// adminDeleteUser issues DELETE /admin/users/{userId} with confirm=true
// and asserts a 2xx, returning the parsed envelope.
func adminDeleteUser(t *testing.T, token, userPublicID string) adminDeleteOutput {
	t.Helper()
	var out adminDeleteOutput
	doJSON(t, http.MethodDelete,
		fmt.Sprintf("%s/admin/users/%s", testServerURL, userPublicID),
		token, map[string]any{"confirm": true}, &out)
	return out
}

// adminDeleteUserStatus is the negative-path counterpart to
// adminDeleteUser.
func adminDeleteUserStatus(t *testing.T, token, userPublicID string, body any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodDelete,
		fmt.Sprintf("%s/admin/users/%s", testServerURL, userPublicID),
		token, body)
}

// errorEnvelope is the subset of the RFC 9457 problem+json envelope the
// nodate-flow handlers emit. The typed catalogue code is exposed under
// the `type` member (the RFC's URI slot, repurposed here for short codes
// like WORKSPACE.DELETE.CONFIRM_REQUIRED). Some legacy paths emit the
// same value under `code`, so this helper accepts both.
type errorEnvelope struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// decodeErrorCode pulls the typed catalogue code out of the response
// body. The handlers emit it under `type` (RFC 9457) on the typed-error
// path and under `code` on the simpler authn 401 path; this helper
// checks both top-level forms and falls back to a nested `errors[]`.
func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var top errorEnvelope
	if err := json.Unmarshal(body, &top); err == nil {
		if top.Type != "" {
			return top.Type
		}
		if top.Code != "" {
			return top.Code
		}
	}
	var nested struct {
		Errors []errorEnvelope `json:"errors"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && len(nested.Errors) > 0 {
		if nested.Errors[0].Type != "" {
			return nested.Errors[0].Type
		}
		if nested.Errors[0].Code != "" {
			return nested.Errors[0].Code
		}
	}
	return ""
}

// grantInstanceAdmin promotes a user to instance admin via direct SQL.
// There is no public bootstrap-style endpoint that can mint an admin
// for an arbitrary user (POST /admin/setup only promotes the calling
// user, and only when no admin exists yet — incompatible with parallel
// tests), so the helper INSERTs into instance_admins itself. This is
// the same exception the audit + sessions admin tests would use.
func grantInstanceAdmin(t *testing.T, db *sql.DB, userPublicID string) {
	t.Helper()
	uid := internalUserID(t, db, userPublicID)
	pid := uuid.Must(uuid.NewV7())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO instance_admins (public_id, user_id, granted_by_user_id)
		 VALUES (UUID_TO_BIN(?, 0), ?, NULL)
		 ON DUPLICATE KEY UPDATE user_id = user_id`,
		pid.String(), uid)
	require.NoError(t, err, "grant instance admin for user %s", userPublicID)
}

// uploadAttachmentForTask creates a task in the supplied tenant's default
// project, presigns + PUTs an attachment, and returns the attachment id
// + storage key for downstream assertions.
func uploadAttachmentForTask(t *testing.T, tt *helpers.TestTenant, label string, payload []byte) (taskID, storageKey string) {
	t.Helper()
	taskID = createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, label)
	res := presignAttachment(t, tt.AccessToken, taskID, label+".png", "image/png", payload)
	require.False(t, res.Deduplicated, "fresh upload must not be deduplicated")
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)
	return taskID, res.StorageKey
}

// uniqueColor builds a color.RGBA seeded by crypto/rand so two tests
// running in parallel never accidentally produce identical PNG payloads
// and hit the dedup path. The 0xFF alpha keeps the resulting PNG opaque
// so the encoded byte stream remains a small fixed size.
func uniqueColor(_ *testing.T) color.RGBA {
	var buf [3]byte
	_, _ = rand.Read(buf[:])
	return color.RGBA{R: buf[0], G: buf[1], B: buf[2], A: 255}
}

// workspaceRowExists reports whether a workspaces row with the given
// internal id is still present.
func workspaceRowExists(t *testing.T, db *sql.DB, wsID uint32) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM workspaces WHERE id = ?`, wsID).Scan(&n))
	return n > 0
}

// userRowExists reports whether a users row with the given internal id
// is still present.
func userRowExists(t *testing.T, db *sql.DB, uid uint32) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = ?`, uid).Scan(&n))
	return n > 0
}

// countAttachmentsForUploader returns the number of attachments rows
// uploaded by a user across every workspace they belonged to.
func countAttachmentsForUploader(t *testing.T, db *sql.DB, uid uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM attachments WHERE uploader_id = ?`, uid).Scan(&n))
	return n
}

// TestAdminDeleteWorkspaceConfirmRequired covers the rejection paths
// for non-confirmed admin delete attempts. All three "missing/false
// confirm" shapes (no body, empty body {}, explicit confirm=false)
// reach the handler and return the same typed 400
// WORKSPACE.DELETE.CONFIRM_REQUIRED.
func TestAdminDeleteWorkspaceConfirmRequired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	t.Run("explicit confirm false", func(t *testing.T) {
		status, body := adminDeleteWorkspaceStatus(t, admin.AccessToken,
			tt.WorkspacePublicID, map[string]any{"confirm": false})
		require.Equalf(t, http.StatusBadRequest, status,
			"confirm=false must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "WORKSPACE.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"confirm=false must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("empty body returns typed confirm-required", func(t *testing.T) {
		status, body := adminDeleteWorkspaceStatus(t, admin.AccessToken,
			tt.WorkspacePublicID, map[string]any{})
		require.Equalf(t, http.StatusBadRequest, status,
			"empty body must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "WORKSPACE.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"empty body must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("no body rejected by schema layer", func(t *testing.T) {
		// Sending no body hits Huma's required-body validation BEFORE
		// the handler runs, producing a generic 400 instead of the
		// typed WORKSPACE.DELETE.CONFIRM_REQUIRED. The admin UI always
		// sends a body; assert only the 400 status to pin the contract
		// that an empty DELETE never destroys data.
		status, body := adminDeleteWorkspaceStatus(t, admin.AccessToken,
			tt.WorkspacePublicID, nil)
		require.Equalf(t, http.StatusBadRequest, status,
			"no body must yield 400; got %d body=%s", status, string(body))
	})

	require.Truef(t, workspaceRowExists(t, testDB, wsID),
		"rejected admin delete must leave the workspaces row intact")
}

// TestAdminDeleteWorkspaceHappyPath drives the full single-step admin
// flow: upload attachments, optionally seed a second tenant whose
// avatar must NOT be touched, then DELETE with confirm=true and assert
//
//   - response reports deleted=true with at least the two attachment
//     storage objects swept and zero MinIO errors,
//   - the workspaces row is gone (CASCADE removed every workspace-scoped
//     row), the MinIO blobs are physically gone, and
//   - the unrelated tenant + their avatar are completely untouched
//     (delete is strictly workspace-scoped).
func TestAdminDeleteWorkspaceHappyPath(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)

	// Two distinct payloads so we get two distinct sha256s and bypass
	// the dedup path; we want two storage_objects rows.
	col1 := uniqueColor(t)
	col2 := uniqueColor(t)
	if col1 == col2 {
		col2.R ^= 0x01
	}
	payload1 := makePNG(t, 8, 8, col1)
	payload2 := makePNG(t, 12, 12, col2)
	require.NotEqual(t, sha256Of(payload1), sha256Of(payload2),
		"payloads must hash differently to produce two storage_objects rows")

	_, key1 := uploadAttachmentForTask(t, tt, "first", payload1)
	_, key2 := uploadAttachmentForTask(t, tt, "second", payload2)
	require.NotEqual(t, key1, key2, "distinct payloads must produce distinct storage keys")

	// Independent tenant + avatar: must survive the workspace delete.
	other := newTenant(t)
	otherAvatar := makePNG(t, 16, 16, uniqueColor(t))
	avBody, avStatus := uploadAvatar(t, other.AccessToken, "other.png", "image/png", otherAvatar)
	require.Equal(t, http.StatusOK, avStatus)
	require.NotNil(t, avBody.AvatarURL)
	otherUID, otherAvatarRow := userInternalIdAndAvatar(t, testDB, other.UserPublicID)
	require.True(t, otherAvatarRow.Valid)
	otherAvatarKey := storageKeyByObjectID(t, testDB, uint32(otherAvatarRow.Int32))
	testStorage.MustExist(t, otherAvatarKey)

	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	require.Equalf(t, 2, countStorageObjectsForWorkspace(t, testDB, wsID),
		"baseline: workspace must own two storage_objects rows after the two uploads")
	testStorage.MustExist(t, key1)
	testStorage.MustExist(t, key2)

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	out := adminDeleteWorkspace(t, admin.AccessToken, tt.WorkspacePublicID)
	require.Truef(t, out.Deleted, "first delete must report deleted=true; got %+v", out)
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(2),
		"delete must report at least the two attachment storage objects; got %d", out.StorageObjectsDeleted)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO must produce zero errors")

	// DB invariants: workspace + every workspace-scoped row gone.
	require.Falsef(t, workspaceRowExists(t, testDB, wsID),
		"delete must hard-delete the workspaces row")
	require.Equalf(t, 0, countStorageObjectsForWorkspace(t, testDB, wsID),
		"workspace CASCADE must remove every storage_objects row")

	// MinIO invariants: workspace blobs gone.
	testStorage.MustNotExist(t, key1)
	testStorage.MustNotExist(t, key2)

	// Unrelated tenant's avatar must be untouched: delete is strictly
	// workspace-scoped.
	require.Truef(t, userRowExists(t, testDB, otherUID),
		"unrelated user must NOT be affected by workspace delete")
	require.Equalf(t, 1, countStorageObjectsForUser(t, testDB, otherUID),
		"unrelated user's avatar storage_objects row must survive")
	testStorage.MustExist(t, otherAvatarKey)
}

// TestAdminDeleteWorkspaceIdempotent calls the admin delete twice in a
// row. Unlike the owner self-delete (where the workspace middleware
// fires first and yields 404), the admin endpoint resolves the workspace
// id itself via AdminFindWorkspaceIdByPublicId. When that query returns
// sql.ErrNoRows the handler short-circuits to a 200 with the zero
// envelope (deleted=false, counts=0) so admin UIs can safely retry
// without flapping between 404 and success.
func TestAdminDeleteWorkspaceIdempotent(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	first := adminDeleteWorkspace(t, admin.AccessToken, tt.WorkspacePublicID)
	require.Truef(t, first.Deleted, "first delete must report deleted=true; got %+v", first)

	second := adminDeleteWorkspace(t, admin.AccessToken, tt.WorkspacePublicID)
	require.Falsef(t, second.Deleted,
		"already-deleted workspace must report deleted=false on retry; got %+v", second)
	require.Equalf(t, int64(0), second.StorageObjectsDeleted,
		"already-deleted workspace must report zero storage objects deleted")
	require.Equalf(t, int64(0), second.MinioErrors,
		"already-deleted workspace must report zero MinIO errors")
}

// TestAdminDeleteWorkspaceNonAdmin guards authorisation: a non-admin
// caller — even the workspace owner — must be denied with 403, and the
// workspace must be left untouched. Two callers exercise the rule:
//
//   - the workspace owner (proves the admin check is not satisfied by
//     workspace ownership), and
//   - a completely unrelated tenant (proves it is not a stale ws-acl
//     check either).
func TestAdminDeleteWorkspaceNonAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)

	t.Run("workspace owner caller", func(t *testing.T) {
		status, body := adminDeleteWorkspaceStatus(t, tt.AccessToken,
			tt.WorkspacePublicID, map[string]any{"confirm": true})
		require.Equalf(t, http.StatusForbidden, status,
			"workspace owner without admin must be denied; got %d body=%s", status, string(body))
	})

	t.Run("stranger caller", func(t *testing.T) {
		stranger := newTenant(t)
		status, body := adminDeleteWorkspaceStatus(t, stranger.AccessToken,
			tt.WorkspacePublicID, map[string]any{"confirm": true})
		require.Equalf(t, http.StatusForbidden, status,
			"unrelated non-admin caller must be denied; got %d body=%s", status, string(body))
	})

	require.Truef(t, workspaceRowExists(t, testDB, wsID),
		"403 must not delete the workspaces row")
}

// TestAdminDeleteUserConfirmRequired covers the rejection paths for
// non-confirmed admin user-delete attempts. All three "missing/false
// confirm" shapes (no body, empty body {}, explicit confirm=false)
// reach the handler and return the same typed 400
// USER.DELETE.CONFIRM_REQUIRED — the dedicated typed code for the user
// endpoint (separate from the workspace one) and the contract the
// admin UI surfaces.
func TestAdminDeleteUserConfirmRequired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	uid := internalUserID(t, testDB, target.UserPublicID)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	t.Run("explicit confirm false", func(t *testing.T) {
		status, body := adminDeleteUserStatus(t, admin.AccessToken,
			target.UserPublicID, map[string]any{"confirm": false})
		require.Equalf(t, http.StatusBadRequest, status,
			"confirm=false must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "USER.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"confirm=false must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("empty body returns typed confirm-required", func(t *testing.T) {
		status, body := adminDeleteUserStatus(t, admin.AccessToken,
			target.UserPublicID, map[string]any{})
		require.Equalf(t, http.StatusBadRequest, status,
			"empty body must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "USER.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"empty body must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("no body rejected by schema layer", func(t *testing.T) {
		// Sending no body hits Huma's required-body validation BEFORE
		// the handler runs, producing a generic 400 instead of the
		// typed USER.DELETE.CONFIRM_REQUIRED. The admin UI always
		// sends a body; assert only the 400 status to pin the contract
		// that an empty DELETE never destroys data.
		status, body := adminDeleteUserStatus(t, admin.AccessToken,
			target.UserPublicID, nil)
		require.Equalf(t, http.StatusBadRequest, status,
			"no body must yield 400; got %d body=%s", status, string(body))
	})

	require.Truef(t, userRowExists(t, testDB, uid),
		"rejected admin delete must leave the users row intact")
}

// TestAdminDeleteUserSelfNotAllowed pins the dedicated self-delete
// rejection: an instance admin trying to delete their own account must
// be rejected with 400 USER.DELETE.SELF_NOT_ALLOWED. This replaces the
// prior overload of VALIDATION.PATH_PARAM.INVALID for this scenario.
func TestAdminDeleteUserSelfNotAllowed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	status, body := adminDeleteUserStatus(t, admin.AccessToken,
		admin.UserPublicID, map[string]any{"confirm": true})
	require.Equalf(t, http.StatusBadRequest, status,
		"self-delete must be rejected with 400; got %d body=%s", status, string(body))
	require.Equalf(t, "USER.DELETE.SELF_NOT_ALLOWED", decodeErrorCode(t, body),
		"self-delete must surface the dedicated typed code; body=%s", string(body))

	uid := internalUserID(t, testDB, admin.UserPublicID)
	require.Truef(t, userRowExists(t, testDB, uid),
		"self-delete rejection must leave the admin user intact")
}

// TestAdminDeleteUserHappyPath end-to-end:
// avatar + workspace attachment uploaded by the target user, admin
// deletes the user with confirm=true, both blobs and the user row are
// gone in one shot.
func TestAdminDeleteUserHappyPath(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	target := newTenant(t)
	avatarPayload := makePNG(t, 16, 16, uniqueColor(t))
	avBody, avStatus := uploadAvatar(t, target.AccessToken, "avatar.png", "image/png", avatarPayload)
	require.Equal(t, http.StatusOK, avStatus)
	require.NotNil(t, avBody.AvatarURL)

	uid, avatarRow := userInternalIdAndAvatar(t, testDB, target.UserPublicID)
	require.True(t, avatarRow.Valid)
	avatarKey := storageKeyByObjectID(t, testDB, uint32(avatarRow.Int32))
	testStorage.MustExist(t, avatarKey)

	// Target also uploads a workspace attachment so we exercise the
	// uploader_id path of the delete sweep.
	attachmentPayload := makePNG(t, 12, 12, uniqueColor(t))
	require.NotEqual(t, sha256Of(attachmentPayload), sha256Of(avatarPayload),
		"attachment and avatar must hash differently")
	_, attKey := uploadAttachmentForTask(t, target, "task-att", attachmentPayload)
	testStorage.MustExist(t, attKey)
	require.Equalf(t, 1, countAttachmentsForUploader(t, testDB, uid),
		"baseline: target must have one uploaded attachment row")

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	out := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.Truef(t, out.Deleted, "user delete must report deleted=true; got %+v", out)
	// Avatar (1) + uploaded attachment whose ref_count was 1 (1) = 2
	// keys swept. Allow >=2 to stay tolerant of additional blobs the
	// handler may later include.
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(2),
		"user delete must sweep at least avatar + sole-referrer attachment; got %d", out.StorageObjectsDeleted)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO must produce zero errors")

	require.Falsef(t, userRowExists(t, testDB, uid),
		"user row must be hard-deleted")
	require.Equalf(t, 0, countStorageObjectsForUser(t, testDB, uid),
		"user-owned storage_objects rows must be gone")
	require.Equalf(t, 0, countAttachmentsForUploader(t, testDB, uid),
		"FK ON DELETE rule on attachments.uploader_id must clear the referring rows")
	testStorage.MustNotExist(t, avatarKey)
	testStorage.MustNotExist(t, attKey)
}

// TestAdminDeleteUserIdempotent pins the in-handler idempotency path:
// AdminFindUserIdByPublicId returning sql.ErrNoRows must short-circuit
// to a 200 with deleted=false (not a 404) so admin UIs can safely
// retry.
func TestAdminDeleteUserIdempotent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	first := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.Truef(t, first.Deleted, "first delete must report deleted=true; got %+v", first)

	second := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.Falsef(t, second.Deleted,
		"already-deleted user must report deleted=false on retry; got %+v", second)
	require.Equalf(t, int64(0), second.StorageObjectsDeleted,
		"already-deleted user must report zero storage objects deleted")
	require.Equalf(t, int64(0), second.MinioErrors,
		"already-deleted user must report zero MinIO errors")
}

// TestAdminDeleteUserNonAdmin guards authorisation: a non-admin caller
// must be denied with 403, regardless of whether they share a workspace
// with the target. The target user must remain intact.
func TestAdminDeleteUserNonAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	uid := internalUserID(t, testDB, target.UserPublicID)
	stranger := newTenant(t)

	status, body := adminDeleteUserStatus(t, stranger.AccessToken,
		target.UserPublicID, map[string]any{"confirm": true})
	require.Equalf(t, http.StatusForbidden, status,
		"non-admin caller must be denied; got %d body=%s", status, string(body))

	require.Truef(t, userRowExists(t, testDB, uid),
		"403 must not delete the users row")
}

// TestAdminDeleteUserSharedAttachment verifies the ref_count rule on
// shared attachment blobs: when two users in the SAME workspace upload
// identical bytes (sha256 dedup → ref_count=2 on one storage_objects
// row), admin-deleting one user must
//
//   - decrement that row's ref_count from 2 to 1,
//   - leave the underlying MinIO blob in place (the surviving user
//     still references it),
//   - hard-delete the deleted user's row.
//
// Then the surviving user deletes their attachment via the standard
// flow → ref_count→0 → MinIO blob removed. That second leg pins that
// the admin delete left the dedup state machine in a consistent,
// GC-able shape.
func TestAdminDeleteUserSharedAttachment(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	owner := newTenant(t)
	other := newTenant(t)

	// Add "other" as a member of owner's workspace via direct SQL: the
	// production path is an invite + accept round-trip but the helper
	// for that mid-test doesn't exist here, and the ref_count guarantee
	// is the same regardless of whether the second member arrived via
	// invite or direct insert. Workspace_members is the only direct-SQL
	// shortcut here; everything else uses the API.
	wsID := internalWorkspaceID(t, testDB, owner.WorkspacePublicID)
	otherUID := internalUserID(t, testDB, other.UserPublicID)
	memberPID := uuid.Must(uuid.NewV7())
	_, err := testDB.ExecContext(context.Background(),
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at, enabled)
		 VALUES (UUID_TO_BIN(?, 0), ?, ?, 'member', NOW(3), TRUE)`,
		memberPID.String(), wsID, otherUID)
	require.NoError(t, err, "seed second workspace member")

	// Both uploads through the same access token (owner) keeps the
	// upload paths simple — the ref_count behaviour is identical
	// regardless of the uploader_id on each row. We then re-attribute
	// the second attachment to "other" via direct SQL to set up the
	// "delete other → ref_count drops, owner's row keeps the blob alive"
	// scenario.
	payload := makePNG(t, 10, 10, uniqueColor(t))
	taskID := createTaskForAttachment(t, owner.AccessToken, owner.ProjectPublicID, "shared")
	first := presignAttachment(t, owner.AccessToken, taskID, "shared-a.png", "image/png", payload)
	require.False(t, first.Deduplicated, "first upload must miss")
	uploadViaPresignedURL(t, first.UploadURL, "image/png", payload, first.RequiredHeaders)

	second := presignAttachment(t, owner.AccessToken, taskID, "shared-b.png", "image/png", payload)
	require.True(t, second.Deduplicated, "identical sha256 must hit the dedup path")
	require.Equal(t, first.StorageKey, second.StorageKey)

	// Re-attribute the second attachment to "other" so deleting "other"
	// decrements ref_count without touching "owner"'s row.
	_, err = testDB.ExecContext(context.Background(),
		`UPDATE attachments SET uploader_id = ?
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		otherUID, wsID, second.AttachmentID)
	require.NoError(t, err, "re-attribute second attachment to other user")

	rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
	require.True(t, ok)
	require.Equalf(t, uint32(2), rc, "baseline: ref_count starts at 2")
	testStorage.MustExist(t, first.StorageKey)

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	out := adminDeleteUser(t, admin.AccessToken, other.UserPublicID)
	require.Truef(t, out.Deleted, "other-delete must report deleted=true; got %+v", out)
	// The shared blob has ref_count==2, so the delete driver's
	// "ref_count == 1 → sweep" rule must NOT include the shared key.
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO + ref_count guard must produce zero errors")

	require.Falsef(t, userRowExists(t, testDB, otherUID),
		"other user must be hard-deleted")

	// The shared storage_objects row MUST survive while the owner still
	// references it. The CASCADE on attachments.uploader_id removed
	// exactly the "other"-owned referring row; ref_count drops 2 → 1.
	rcAfter, ok2 := storageObjectRefCount(t, testDB, first.StorageKey)
	require.Truef(t, ok2,
		"shared storage_objects row must survive while owner still references it")
	require.Equalf(t, uint32(1), rcAfter,
		"ref_count must be decremented from 2 to 1 by the delete handler")
	require.Equalf(t, 1, countAttachmentsForStorageKey(t, testDB, first.StorageKey),
		"exactly one attachment row (owner's) must remain pointing at the shared blob")

	// MinIO blob must still exist — the surviving owner can still
	// download their attachment. This is the load-bearing user-facing
	// invariant of the ref_count rule.
	testStorage.MustExist(t, first.StorageKey)

	// Final guarantee: the standard attachment-delete flow on the
	// surviving owner MUST drive the now-sole-referrer ref_count from
	// 1 to 0 and trip the GC path that drops both the storage_objects
	// row and the MinIO blob. This proves the admin-delete left the
	// dedup state machine in a consistent, GC-able shape.
	status, body := doJSONStatus(t, http.MethodDelete,
		fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskID, first.AttachmentID),
		owner.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"owner must be able to delete their attachment via the standard flow; body=%s", string(body))

	_, okAfterDelete := storageObjectRefCount(t, testDB, first.StorageKey)
	require.Falsef(t, okAfterDelete,
		"storage_objects row must be GC'd when ref_count hits 0")
	require.Equalf(t, 0, countAttachmentsForStorageKey(t, testDB, first.StorageKey),
		"no attachment rows must remain after owner deletes their copy")
	testStorage.MustNotExist(t, first.StorageKey)
}
