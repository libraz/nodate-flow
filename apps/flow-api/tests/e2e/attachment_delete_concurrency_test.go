package e2e

import (
	"fmt"
	"image/color"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConcurrentDeletesOnOneTaskDoNotDeadlock is serial on purpose: it
// swaps the process-wide default logger to count deadlock retries.
func TestConcurrentDeletesOnOneTaskDoNotDeadlock(t *testing.T) {
	bootstrap(t)
	requireStorage(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Concurrent deletes")

	const n = 8
	attachmentIDs := make([]string, n)
	for i := range attachmentIDs {
		payload := makePNG(t, 8, 8, color.RGBA{R: 101, G: 102, B: uint8(i), A: 255}) //#nosec G115 -- i < n
		res := presignAttachment(t, tt.AccessToken, taskID, fmt.Sprintf("del-%d.png", i), "image/png", payload)
		uploadViaPresignedURL(t, res.UploadURL, "image/png", payload)
		confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)
		attachmentIDs[i] = res.AttachmentID
	}

	retries := countRetries(t, "tasks.DeleteAttachment")
	statuses := make([]int, n)
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range attachmentIDs {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			statuses[i], _, errs[i] = sendJSONStatus(http.MethodDelete,
				fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskID, attachmentIDs[i]), tt.AccessToken, nil)
		}(i)
	}
	start.Done()
	done.Wait()

	for i := range statuses {
		require.NoErrorf(t, errs[i], "delete %d", i)
		require.Equalf(t, http.StatusOK, statuses[i], "delete %d", i)
	}
	require.Zero(t, retries.Load(), "deletes on one task must serialize on the task row, not deadlock and retry")
}
