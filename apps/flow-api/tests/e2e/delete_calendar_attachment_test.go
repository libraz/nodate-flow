// Calendar-attachment coverage for the immediate-delete pipeline.
//
// The existing admin_delete_test.go suite proves the contract on TASK
// attachments (storage_objects sweep on workspace delete, ref_count
// decrement + sole-referrer GC on user delete). Calendar attachments
// live in a separate table (calendar_event_attachments) but share the
// storage_objects machinery — and the user-delete teardown pipeline
// explicitly enumerates them via ListCalendarEventAttachmentsForUploaderPurge.
// This file closes the regression gap so the calendar uploader path is
// covered by tests with the same rigour as the task uploader path.
//
// Two subtests:
//
//   - WorkspaceDeleteSweepsCalendarAttachments — workspace owner self-delete
//     wipes every calendar_event_attachments row, the underlying
//     storage_objects row, AND the physical MinIO blob.
//   - AdminDeleteUserSpansTaskAndCalendarUploaders — admin user-delete
//     decrements a shared blob's ref_count when one referrer is a task
//     attachment and the other is a calendar event attachment, leaving
//     the blob alive while the surviving user still references it.
//     This explicitly exercises the path that
//     TestAdminDeleteUserSharedAttachment proves for two task uploaders
//     but does NOT prove for the heterogeneous task+calendar case.
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"image/color"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// countCalendarEventAttachmentsByPublicID returns 1 when the
// calendar_event_attachments row with the given public id exists, 0
// otherwise. Direct SQL because the API has no "does this attachment
// row exist?" endpoint exposed to admins.
func countCalendarEventAttachmentsByPublicID(t *testing.T, db *sql.DB, attPublicID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_event_attachments
		  WHERE public_id = UUID_TO_BIN(?, 0)`,
		attPublicID).Scan(&n)
	require.NoError(t, err)
	return n
}

// countCalendarEventsByPublicID is the calendar-events counterpart to
// the helper above. Used to assert workspace delete CASCADEs through
// calendars → calendar_events.
func countCalendarEventsByPublicID(t *testing.T, db *sql.DB, evtPublicID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_events
		  WHERE public_id = UUID_TO_BIN(?, 0)`,
		evtPublicID).Scan(&n)
	require.NoError(t, err)
	return n
}

// seedWorkspaceMember inserts a workspace_members row via direct SQL.
// Same exception as in TestAdminDeleteUserSharedAttachment: the invite
// + accept round-trip would work but adds noise the delete-pipeline
// assertions do not care about; the ref_count behaviour is identical
// regardless of how the member arrived.
func seedWorkspaceMember(t *testing.T, db *sql.DB, wsID, userID uint32, role string) {
	t.Helper()
	memberPID := uuid.Must(uuid.NewV7())
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at, enabled)
		 VALUES (UUID_TO_BIN(?, 0), ?, ?, ?, NOW(3), TRUE)`,
		memberPID.String(), wsID, userID, role)
	require.NoError(t, err, "seed workspace member uid=%d ws=%d role=%s", userID, wsID, role)
}

// createCalendarForTenant creates a personal calendar in the given
// tenant's workspace using their access token. Returns the calendar
// public id. Mirrors createCalendarForAttachments but takes a raw token
// + workspace id so it can be used with non-owner members too.
func createCalendarForTenant(t *testing.T, token, wsPublicID, name string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+wsPublicID+"/calendars",
		token, map[string]any{
			"kind":  "personal",
			"name":  name,
			"color": "#34A853",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create calendar must return public id")
	return resp.ID
}

// createCalendarEventForTenant creates a one-hour event on a calendar
// using the given token.
func createCalendarEventForTenant(t *testing.T, token, wsPublicID, calID, title string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	start := time.Date(2027, 8, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events",
			testServerURL, wsPublicID, calID),
		token, map[string]any{
			"kind":     "event",
			"title":    title,
			"startAt":  start.Unix(),
			"endAt":    end.Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return public id")
	return resp.ID
}

// TestWorkspaceDeleteSweepsCalendarAttachments drives the full
// calendar-attachment cleanup path through the workspace owner self
// delete endpoint:
//
//  1. Create calendar + event in the tenant's workspace.
//  2. Upload an attachment (presign + PUT) so a storage_objects row +
//     calendar_event_attachments row + MinIO blob all exist.
//  3. DELETE /workspaces/{wsId} with {confirm: true}.
//  4. Assert: every row gone (calendar_event_attachments via FK
//     CASCADE on workspace_id, storage_objects via the same), and the
//     MinIO blob is physically removed by the workspace teardown sweep.
//
// This is the calendar analogue of TestOwnerDeleteWorkspaceHappyPath's
// task-attachment assertion: same single API call, same single-step
// destructive contract, exercised against the calendar surface.
func TestWorkspaceDeleteSweepsCalendarAttachments(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)

	calID := createCalendarForTenant(t, tt.AccessToken, tt.WorkspacePublicID, "DeleteSweep "+randomHex(4))
	evtID := createCalendarEventForTenant(t, tt.AccessToken, tt.WorkspacePublicID, calID, "DeleteSweep")

	payload := makePNG(t, 10, 10, uniqueColor(t))
	persona := tenantPersona{tok: tt.AccessToken, ws: tt.WorkspacePublicID}
	pres := presignCalendarAttachment(t, persona, calID, evtID, "cal-att.png", "image/png", payload)
	require.Falsef(t, pres.Deduplicated, "fresh upload must not dedup")
	uploadViaPresignedURL(t, pres.UploadURL, "image/png", payload, pres.RequiredHeaders)
	testStorage.MustExist(t, pres.StorageKey)
	require.Equalf(t, 1,
		countCalendarEventAttachmentsByPublicID(t, testDB, pres.AttachmentID),
		"baseline: calendar attachment row must be present pre-delete")
	require.Equalf(t, 1, countStorageObjectsForWorkspace(t, testDB, wsID),
		"baseline: workspace must own exactly one storage_objects row from the calendar upload")

	// Single-step destructive delete: owner self-service.
	var out adminDeleteOutput
	doJSON(t, http.MethodDelete,
		fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, map[string]any{"confirm": true}, &out)
	require.Truef(t, out.Deleted, "workspace delete must report deleted=true; got %+v", out)
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(1),
		"delete must report at least the one calendar attachment storage object; got %d",
		out.StorageObjectsDeleted)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO must produce zero errors")

	// DB CASCADE: calendar event, attachment row, storage_objects row all gone.
	require.Equalf(t, 0,
		countCalendarEventAttachmentsByPublicID(t, testDB, pres.AttachmentID),
		"workspace delete must CASCADE through calendar_event_attachments")
	require.Equalf(t, 0,
		countCalendarEventsByPublicID(t, testDB, evtID),
		"workspace delete must CASCADE through calendars → calendar_events")
	require.Equalf(t, 0, countStorageObjectsForWorkspace(t, testDB, wsID),
		"workspace delete must remove every workspace-scoped storage_objects row")

	// MinIO sweep: the blob must be physically gone.
	testStorage.MustNotExist(t, pres.StorageKey)
}

// TestAdminDeleteUserSpansTaskAndCalendarUploaders proves the ref_count
// machinery treats a heterogeneous task+calendar pair of referrers the
// same way TestAdminDeleteUserSharedAttachment proves it for two task
// referrers: the surviving non-task referrer (calendar attachment) keeps
// the blob alive after the task uploader is force-deleted.
//
// Shape:
//
//  1. Owner (User A) creates the workspace.
//  2. User B is inserted as a workspace_members row (direct SQL).
//  3. User A uploads a TASK attachment with payload P → storage_objects
//     row created, ref_count == 1.
//  4. User B presigns a CALENDAR attachment with the SAME payload P →
//     sha256 dedup hit, ref_count == 2. (Calendar dedup is documented
//     in TestCalendarPresignDedupHit but here we cross the seam: one
//     task referrer + one calendar referrer.)
//  5. Admin deletes User A. teardown.User enumerates A's task
//     attachments only (A uploaded none on the calendar side), decrements
//     ref_count once, and the post-CASCADE DeleteStorageObjectIfUnreferenced
//     no-ops because ref_count == 1 (B's calendar row survives).
//
// Asserts: A's task attachment row gone, storage_objects row survives
// with ref_count == 1, MinIO blob still present. Then B deletes their
// calendar attachment through the standard API → ref_count → 0 → row +
// blob gone.
//
// Mirrors TestAdminDeleteUserSharedAttachment but proves the calendar
// uploader pipeline goes through the same teardown code path.
func TestAdminDeleteUserSpansTaskAndCalendarUploaders(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	owner := newTenant(t)
	other := newTenant(t)

	// User B (other) becomes a workspace_members row in A's workspace.
	wsID := internalWorkspaceID(t, testDB, owner.WorkspacePublicID)
	ownerUID := internalUserID(t, testDB, owner.UserPublicID)
	otherUID := internalUserID(t, testDB, other.UserPublicID)
	seedWorkspaceMember(t, testDB, wsID, otherUID, "member")

	// Single payload so the second upload hits the sha256 dedup branch
	// across the task/calendar boundary.
	payload := makePNG(t, 12, 12, color.RGBA{R: 137, G: 200, B: 50, A: 255})

	// Leg 1: A uploads a TASK attachment.
	ownerTaskID, ownerStorageKey := uploadAttachmentForTask(t, owner, "shared-task", payload)
	_ = ownerTaskID
	rc, ok := storageObjectRefCount(t, testDB, ownerStorageKey)
	require.Truef(t, ok, "storage_objects row must exist after first upload")
	require.Equalf(t, uint32(1), rc, "ref_count after task upload must be 1; got %d", rc)
	testStorage.MustExist(t, ownerStorageKey)

	// Leg 2: B presigns a CALENDAR attachment with the same bytes ->
	// dedup hit on the workspace's sha256 row.
	bPersona := tenantPersona{tok: other.AccessToken, ws: owner.WorkspacePublicID}
	bCalID := createCalendarForTenant(t, other.AccessToken, owner.WorkspacePublicID, "B-cal "+randomHex(4))
	bEvtID := createCalendarEventForTenant(t, other.AccessToken, owner.WorkspacePublicID, bCalID, "B-cal-evt")
	bAtt := presignCalendarAttachment(t, bPersona, bCalID, bEvtID, "shared-cal.png", "image/png", payload)
	require.Truef(t, bAtt.Deduplicated,
		"identical bytes across task + calendar in the same workspace must dedup")
	require.Equalf(t, ownerStorageKey, bAtt.StorageKey,
		"dedup must reuse the same storage_key across the task/calendar boundary")

	rc, ok = storageObjectRefCount(t, testDB, ownerStorageKey)
	require.True(t, ok)
	require.Equalf(t, uint32(2), rc,
		"ref_count after dedup hit must be 2 (1 task + 1 calendar); got %d", rc)

	// Admin force-deletes User A. teardown.User decrements once via
	// ListAttachmentsForUploaderPurge (the task row A owns), runs
	// HardDeleteUser, then DeleteStorageObjectIfUnreferenced no-ops
	// because ref_count == 1 (B's calendar attachment survives).
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	out := adminDeleteUser(t, admin.AccessToken, owner.UserPublicID)
	require.Truef(t, out.Deleted, "admin user-delete must report deleted=true; got %+v", out)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO + ref_count guard must produce zero errors")

	// A's task attachment row is gone (CASCADE on uploader_id).
	require.Falsef(t, userRowExists(t, testDB, ownerUID),
		"target user (A) must be hard-deleted")
	require.Equalf(t, 0, countAttachmentsForUploader(t, testDB, ownerUID),
		"FK ON DELETE CASCADE must clear A's task attachment row")

	// storage_objects row survives — B still references it via the
	// calendar attachment.
	rcAfter, okAfter := storageObjectRefCount(t, testDB, ownerStorageKey)
	require.Truef(t, okAfter,
		"shared storage_objects row must survive while B's calendar attachment still references it")
	require.Equalf(t, uint32(1), rcAfter,
		"ref_count must drop from 2 to 1 (one referrer removed, one survives); got %d", rcAfter)
	require.Equalf(t, 1,
		countCalendarEventAttachmentsByPublicID(t, testDB, bAtt.AttachmentID),
		"B's calendar attachment row must survive A's delete")
	testStorage.MustExist(t, ownerStorageKey)

	// Final leg: B deletes their calendar attachment via the standard
	// API. ref_count → 0 → DeleteStorageObjectIfUnreferenced removes the
	// row, and the calendar handler's MinIO sweep deletes the blob.
	delURL := fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s",
		testServerURL, owner.WorkspacePublicID, bCalID, bEvtID, bAtt.AttachmentID)
	status, body := doJSONStatus(t, http.MethodDelete, delURL, other.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"surviving calendar attachment must be deletable via the standard flow; body=%s",
		string(body))

	_, stillThere := storageObjectRefCount(t, testDB, ownerStorageKey)
	require.Falsef(t, stillThere,
		"storage_objects row must be GC'd when ref_count hits 0 after B's delete")
	require.Equalf(t, 0,
		countCalendarEventAttachmentsByPublicID(t, testDB, bAtt.AttachmentID),
		"B's calendar attachment row must be gone after the API delete")
	testStorage.MustNotExist(t, ownerStorageKey)
}
