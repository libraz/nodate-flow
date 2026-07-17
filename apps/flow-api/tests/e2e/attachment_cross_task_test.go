// Cross-task attachment scoping suite. Complements
// attachment_security_test.go, which pins the cross-workspace boundary;
// this file pins the tighter cross-task boundary WITHIN a single
// workspace.
//
// An attachment id lives in the URL only under a task path
// (/tasks/{id}/attachments/{aid}). The download and delete queries scope
// on task_id in addition to workspace_id, so an attachment belonging to
// task B cannot be dereferenced (downloaded) or removed (deleted) through
// task A's path even though both tasks share the workspace and the caller
// holds access to both. A mismatch resolves to 404 WS.TASK.NOT_FOUND so
// cross-task existence stays hidden.
package e2e

import (
	"fmt"
	"image/color"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCrossTaskAttachmentAccess verifies that task_id scoping on the
// attachment download / delete queries blocks a caller from reaching
// task B's attachment through task A's URL path, even when the caller is
// a member of the workspace and owns both tasks. The mismatch must
// return 404 (not 200 idempotent success and not a 5xx), and the
// attachment must remain intact after the rejected probes.
func TestCrossTaskAttachmentAccess(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskA := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "cross-task-a")
	taskB := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "cross-task-b")
	require.NotEqual(t, taskA, taskB)

	// Upload an attachment to task B and physically store the blob so
	// the download path has real bytes to serve.
	payload := makePNG(t, 10, 10, color.RGBA{R: 60, G: 120, B: 180, A: 255})
	res := presignAttachment(t, tt.AccessToken, taskB, "b-secret.png", "image/png", payload)
	require.False(t, res.Deduplicated)
	require.NotEmpty(t, res.AttachmentID)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

	// Positive control: the attachment is genuinely reachable through
	// its own task path, so the cross-task 404s below prove scoping
	// rather than a non-existent id.
	t.Run("download through owning task succeeds", func(t *testing.T) {
		var out struct {
			DownloadURL string `json:"downloadUrl"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments/%s/download", testServerURL, taskB, res.AttachmentID),
			tt.AccessToken, nil, &out)
		require.NotEmpty(t, out.DownloadURL, "owning task must mint a download URL")
	})

	t.Run("download through sibling task returns 404", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments/%s/download", testServerURL, taskA, res.AttachmentID),
			tt.AccessToken, nil)
		require.Equalf(t, http.StatusNotFound, status,
			"cross-task download must be 404; body=%s", string(body))
		require.Containsf(t, string(body), "WS.TASK.NOT_FOUND",
			"cross-task download must hide existence via WS.TASK.NOT_FOUND; body=%s", string(body))
		require.NotContainsf(t, string(body), res.StorageKey,
			"cross-task download must not leak the storage key; body=%s", string(body))
	})

	t.Run("delete through sibling task returns 404 and leaves the attachment intact", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskA, res.AttachmentID),
			tt.AccessToken, nil)
		require.Equalf(t, http.StatusNotFound, status,
			"cross-task delete must be 404, not idempotent 200; body=%s", string(body))
		require.Containsf(t, string(body), "WS.TASK.NOT_FOUND",
			"cross-task delete must hide existence via WS.TASK.NOT_FOUND; body=%s", string(body))

		// The rejected delete must not have mutated state: the storage
		// object row and its ref count survive untouched.
		rc, ok := storageObjectRefCount(t, testDB, res.StorageKey)
		require.True(t, ok, "storage_objects row must survive the cross-task delete probe")
		require.Equalf(t, uint32(1), rc, "ref_count must be untouched by the rejected cross-task delete")
	})

	// Final positive control: the legitimate owner (task B path) can
	// still delete the attachment, confirming the scoping did not break
	// the happy path.
	t.Run("delete through owning task succeeds", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskB, res.AttachmentID),
			tt.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status,
			"owning-task delete must succeed; body=%s", string(body))
	})
}
