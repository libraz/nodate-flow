package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/color"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// taskWorkRevision reads the revision a review approval is pinned to.
func taskWorkRevision(t *testing.T, taskPublicID string) uint32 {
	t.Helper()
	var rev uint32
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT work_revision FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, taskPublicID).Scan(&rev))
	return rev
}

// retryCounter counts the transient-error retries dbretry logs for one
// operation, forwarding every record to the handler it wraps.
type retryCounter struct {
	slog.Handler
	op string
	n  *atomic.Int64
}

func (h retryCounter) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "dbretry: transient mysql error, retrying" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "op" && a.Value.String() == h.op {
				h.n.Add(1)
				return false
			}
			return true
		})
	}
	return h.Handler.Handle(ctx, r)
}

func (h retryCounter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return retryCounter{Handler: h.Handler.WithAttrs(attrs), op: h.op, n: h.n}
}

func (h retryCounter) WithGroup(name string) slog.Handler {
	return retryCounter{Handler: h.Handler.WithGroup(name), op: h.op, n: h.n}
}

// countRetries routes the default logger through a retryCounter for op
// until the test ends. The inner handler is a fresh one: wrapping the
// built-in default handler deadlocks once SetDefault redirects package log.
func countRetries(t *testing.T, op string) *atomic.Int64 {
	t.Helper()
	prev := slog.Default()
	n := &atomic.Int64{}
	slog.SetDefault(slog.New(retryCounter{Handler: slog.NewTextHandler(os.Stderr, nil), op: op, n: n}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return n
}

func TestAbandonedPresignLeavesWorkRevisionAlone(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Abandoned presign")
	before := taskWorkRevision(t, taskID)

	payload := makePNG(t, 8, 8, color.RGBA{R: 71, G: 72, B: 73, A: 255})
	res := presignAttachment(t, tt.AccessToken, taskID, "abandoned.png", "image/png", payload)
	require.Equal(t, before, taskWorkRevision(t, taskID), "a reservation is not work on the task")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	past := time.Now().UTC().Add(-2 * time.Hour)
	_, err := testDB.ExecContext(ctx,
		`UPDATE storage_objects SET created_at = ?, updated_at = ? WHERE storage_key = ?`, past, past, res.StorageKey)
	require.NoError(t, err)
	sweeper := newSweeper(t)
	sweeper.ReservationTTL = time.Hour
	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, storageObjectExists(ctx, t, res.StorageKey), "the sweep reclaims the reservation")

	require.Equal(t, before, taskWorkRevision(t, taskID), "reclaiming a never-uploaded file must not invalidate an approval")
}

func TestConfirmedUploadAndItsDeletionBumpWorkRevision(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Uploaded work")
	payload := makePNG(t, 8, 8, color.RGBA{R: 81, G: 82, B: 83, A: 255})
	res := presignAttachment(t, tt.AccessToken, taskID, "work.png", "image/png", payload)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload)
	beforeConfirm := taskWorkRevision(t, taskID)

	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)
	afterConfirm := taskWorkRevision(t, taskID)
	require.Equal(t, beforeConfirm+1, afterConfirm, "the file arriving changes the reviewed work")

	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)
	require.Equal(t, afterConfirm, taskWorkRevision(t, taskID), "a repeated confirm is not a second change")

	status, body := doJSONStatus(t, http.MethodDelete,
		fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskID, res.AttachmentID), tt.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "delete attachment; body=%s", string(body))
	require.Equal(t, afterConfirm+1, taskWorkRevision(t, taskID), "removing an uploaded file changes the reviewed work")
}

// TestConcurrentPresignsOnOneTaskDoNotDeadlock is serial on purpose: it
// swaps the process-wide default logger to count deadlock retries.
func TestConcurrentPresignsOnOneTaskDoNotDeadlock(t *testing.T) {
	bootstrap(t)
	requireStorage(t)

	tt := newTenant(t)
	sourceTask := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Upload source")
	targetTask := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Concurrent target")

	// Stored content makes every presign below a dedup hit on its own row, so
	// each one attaches real work to the target task.
	const n = 8
	payloads := make([][]byte, n)
	for i := range payloads {
		payloads[i] = makePNG(t, 8, 8, color.RGBA{R: 91, G: 92, B: uint8(i), A: 255}) //#nosec G115 -- i < n
		res := presignAttachment(t, tt.AccessToken, sourceTask, fmt.Sprintf("src-%d.png", i), "image/png", payloads[i])
		uploadViaPresignedURL(t, res.UploadURL, "image/png", payloads[i])
		confirmAttachment(t, tt.AccessToken, sourceTask, res.AttachmentID)
	}

	retries := countRetries(t, "tasks.PresignUpload")
	statuses := make([]int, n)
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range payloads {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			sum := sha256.Sum256(payloads[i])
			start.Wait()
			statuses[i], _, errs[i] = sendJSONStatus(http.MethodPost,
				fmt.Sprintf("%s/tasks/%s/attachments/presign", testServerURL, targetTask), tt.AccessToken,
				map[string]any{
					"filename":    fmt.Sprintf("dst-%d.png", i),
					"contentType": "image/png",
					"byteSize":    len(payloads[i]),
					"sha256":      hex.EncodeToString(sum[:]),
				})
		}(i)
	}
	start.Done()
	done.Wait()

	for i := range statuses {
		require.NoErrorf(t, errs[i], "presign %d", i)
		require.Equalf(t, http.StatusOK, statuses[i], "presign %d", i)
	}
	require.Zero(t, retries.Load(), "presigns on one task must serialize on the task row, not deadlock and retry")
}
