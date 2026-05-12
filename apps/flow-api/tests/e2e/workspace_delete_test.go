// E2E coverage for the workspace owner self-service immediate destructive
// delete endpoint:
//
//	DELETE /workspaces/{wsId}
//
// served by auth-api and reached through the composite test handler.
//
// Contract (single-step, no soft-disable precondition):
//
//   - Owner role required (enforced upstream by RequireWorkspaceRole).
//   - Body MUST be {"confirm": true}; missing or false returns 400
//     WORKSPACE.DELETE.CONFIRM_REQUIRED.
//   - Response is 200 with {deleted, storageObjectsDeleted, minioErrors}.
//   - The handler bulk-removes every MinIO blob owned by the workspace,
//     then issues a CASCADE-anchored hard DELETE on the workspaces row.
//   - Idempotency: the in-handler "deleted=false on RowsAffected==0"
//     path is unreachable through the public route because the workspace
//     middleware fires first and returns 404 WS.WORKSPACE.NOT_FOUND on
//     the missing row. TestOwnerDeleteWorkspaceIdempotentRetry pins the
//     observed behaviour so any future contract change is caught.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// deleteWorkspaceOutput mirrors the response envelope for the workspace
// owner self-delete endpoint. Kept local to this file so the assertions
// read end-to-end without import gymnastics.
type deleteWorkspaceOutput struct {
	Deleted               bool  `json:"deleted"`
	StorageObjectsDeleted int64 `json:"storageObjectsDeleted"`
	MinioErrors           int64 `json:"minioErrors"`
}

// TestOwnerDeleteWorkspaceConfirmRequired covers the rejection paths
// for non-confirmed delete attempts. All three "missing/false confirm"
// shapes (no body, empty body {}, explicit confirm=false) reach the
// handler and return the same typed 400 WORKSPACE.DELETE.CONFIRM_REQUIRED.
func TestOwnerDeleteWorkspaceConfirmRequired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	url := fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID)

	t.Run("explicit confirm false", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken,
			map[string]any{"confirm": false})
		require.Equalf(t, http.StatusBadRequest, status,
			"confirm=false must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "WORKSPACE.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"confirm=false must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("empty body returns typed confirm-required", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken,
			map[string]any{})
		require.Equalf(t, http.StatusBadRequest, status,
			"empty body must yield 400 from the handler; got %d body=%s", status, string(body))
		require.Equalf(t, "WORKSPACE.DELETE.CONFIRM_REQUIRED", decodeErrorCode(t, body),
			"empty body must surface the typed catalogue code; body=%s", string(body))
	})

	t.Run("no body rejected by schema layer", func(t *testing.T) {
		// Sending no body at all (no Content-Type, no payload) hits
		// Huma's required-body validation BEFORE the handler runs, so
		// the response is a generic 400 "request body is required"
		// rather than the typed WORKSPACE.DELETE.CONFIRM_REQUIRED code.
		// The frontend always sends {confirm: true|false} so this path
		// is exercised only by hand-crafted requests; assert just the
		// 400 status to pin the contract that an empty DELETE never
		// destroys data, without over-specifying the error shape.
		status, body := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken, nil)
		require.Equalf(t, http.StatusBadRequest, status,
			"no body must yield 400; got %d body=%s", status, string(body))
	})

	require.Truef(t, workspaceRowExists(t, testDB, wsID),
		"rejected delete must leave the workspaces row intact")
}

// TestOwnerDeleteWorkspaceHappyPath drives the full single-step flow:
// upload an attachment so a storage_objects row + MinIO blob exists,
// then DELETE with confirm=true and assert that
//
//   - the response reports deleted=true with at least one storage object
//     swept and zero MinIO errors,
//   - the workspaces row is gone, every workspace-scoped sub-resource is
//     no longer reachable through its respective endpoint, and
//   - the underlying MinIO blob is physically removed.
func TestOwnerDeleteWorkspaceHappyPath(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)

	// Upload one real attachment so the storage_objects row + MinIO blob
	// give us something concrete to verify is gone after the delete.
	payload := makePNG(t, 8, 8, uniqueColor(t))
	_, storageKey := uploadAttachmentForTask(t, tt, "delete-target", payload)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	require.Equalf(t, 1, countStorageObjectsForWorkspace(t, testDB, wsID),
		"baseline: workspace must own exactly one storage_objects row")
	testStorage.MustExist(t, storageKey)

	// Single-step destructive delete. No soft-disable precondition.
	var out deleteWorkspaceOutput
	doJSON(t, http.MethodDelete,
		fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, map[string]any{"confirm": true}, &out)
	require.Truef(t, out.Deleted, "delete must report deleted=true; got %+v", out)
	require.GreaterOrEqualf(t, out.StorageObjectsDeleted, int64(1),
		"delete must report at least the one attachment storage object; got %d", out.StorageObjectsDeleted)
	require.Equalf(t, int64(0), out.MinioErrors,
		"healthy MinIO must produce zero errors; got %d", out.MinioErrors)

	// Workspace row + every workspace-scoped row gone.
	require.Falsef(t, workspaceRowExists(t, testDB, wsID),
		"delete must hard-delete the workspaces row")
	require.Equalf(t, 0, countStorageObjectsForWorkspace(t, testDB, wsID),
		"workspace CASCADE must remove every storage_objects row")

	// MinIO blob physically gone.
	testStorage.MustNotExist(t, storageKey)

	// Workspace endpoint and every sub-resource endpoint must reject.
	// The workspace middleware fires first and emits 404
	// WS.WORKSPACE.NOT_FOUND because the workspaces row is gone.
	status, body := doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil)
	require.GreaterOrEqualf(t, status, 400,
		"deleted workspace GET must not be 2xx; got %d body=%s", status, string(body))
	require.Lessf(t, status, 500,
		"deleted workspace GET must be 4xx (not 5xx); got %d body=%s", status, string(body))

	status, body = doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/tasks?workspaceId=%s", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil)
	require.GreaterOrEqualf(t, status, 400,
		"tasks list scoped to deleted workspace must not be 2xx; got %d body=%s", status, string(body))
	require.Lessf(t, status, 500,
		"tasks list scoped to deleted workspace must be 4xx (not 5xx); got %d body=%s", status, string(body))

	status, body = doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/projects", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil)
	require.GreaterOrEqualf(t, status, 400,
		"projects list under deleted workspace must not be 2xx; got %d body=%s", status, string(body))
	require.Lessf(t, status, 500,
		"projects list under deleted workspace must be 4xx (not 5xx); got %d body=%s", status, string(body))
}

// TestOwnerDeleteWorkspaceIdempotentRetry pins the post-delete repeat
// behaviour. After the workspaces row is gone, the workspace middleware
// fires before the handler and emits 404 WS.WORKSPACE.NOT_FOUND, so the
// "deleted=false" idempotency path of the handler itself is unreachable
// through the public route. The test pins the observed 404 contract so
// any future change (for example, moving the lookup into the handler so
// it returns 200 deleted=false) is caught by CI.
func TestOwnerDeleteWorkspaceIdempotentRetry(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	url := fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID)

	// First delete must succeed.
	var first deleteWorkspaceOutput
	doJSON(t, http.MethodDelete, url, tt.AccessToken,
		map[string]any{"confirm": true}, &first)
	require.True(t, first.Deleted, "first delete must report deleted=true")

	// Second delete: the workspace middleware now finds no row and
	// emits 404 WS.WORKSPACE.NOT_FOUND BEFORE the handler runs, so the
	// in-handler idempotency path (200 deleted=false) is never reached.
	// Pin the actual observed behaviour so a regression is loud.
	status, body := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken,
		map[string]any{"confirm": true})
	require.Equalf(t, http.StatusNotFound, status,
		"repeat delete must hit the workspace middleware and 404; got %d body=%s",
		status, string(body))

	// And just to be sure the second-call body isn't accidentally a
	// success envelope: it must NOT decode as deleted=true.
	var maybeOut deleteWorkspaceOutput
	if err := json.Unmarshal(body, &maybeOut); err == nil {
		require.Falsef(t, maybeOut.Deleted,
			"repeat delete must not surface deleted=true; got %+v", maybeOut)
	}
}

// TestOwnerDeleteWorkspaceWrongConfirmType asserts that a non-bool
// confirm value (number, string) is rejected by the schema layer with a
// validation error rather than silently treated as "missing".
//
// This guards a class of typo bugs where a client sends {"confirm": 1}
// or {"confirm": "true"} expecting truthiness — Huma's strict typing
// makes that a 422, NOT a successful delete.
func TestOwnerDeleteWorkspaceWrongConfirmType(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	url := fmt.Sprintf("%s/workspaces/%s", testServerURL, tt.WorkspacePublicID)

	// Send `confirm: "true"` (string, not bool). Huma must reject this
	// as an invalid type at the schema layer; the handler must NOT see
	// it as a truthy value.
	status, body := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken,
		map[string]any{"confirm": "true"})
	require.GreaterOrEqualf(t, status, 400,
		"non-bool confirm must be rejected; got %d body=%s", status, string(body))
	require.Lessf(t, status, 500,
		"non-bool confirm must be a 4xx, not 5xx; got %d body=%s", status, string(body))

	require.Truef(t, workspaceRowExists(t, testDB, wsID),
		"validation rejection must leave the workspaces row intact")
}
