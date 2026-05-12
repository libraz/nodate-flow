package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// makePNG returns a deterministic PNG payload of the requested width
// and height filled with a single colour. Callers vary the colour to
// produce two payloads with two different sha256 hashes when they need
// to test the "miss" path.
func makePNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img), "encode png")
	return buf.Bytes()
}

// presignAttachment hits POST /tasks/{id}/attachments/presign and
// returns the parsed body. Failure surfaces as a test failure.
func presignAttachment(t *testing.T, token, taskID, filename, contentType string, payload []byte) presignResponse {
	t.Helper()
	sum := sha256.Sum256(payload)
	body := map[string]any{
		"filename":    filename,
		"contentType": contentType,
		"byteSize":    len(payload),
		"sha256":      hex.EncodeToString(sum[:]),
	}
	var out presignResponse
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/tasks/%s/attachments/presign", testServerURL, taskID),
		token, body, &out)
	return out
}

type presignResponse struct {
	UploadURL       string            `json:"uploadUrl"`
	StorageKey      string            `json:"storageKey"`
	AttachmentID    string            `json:"attachmentId"`
	Deduplicated    bool              `json:"deduplicated"`
	RequiredHeaders map[string]string `json:"requiredHeaders"`
}

// uploadViaPresignedURL streams the bytes to the supplied presigned PUT
// URL and asserts a 2xx response. The upload step belongs to the client
// in production; the test simulates that step so the MinIO blob is
// physically present when the test inspects ref counts.
//
// requiredHeaders are the entries the server returned alongside the
// presigned URL (currently x-amz-content-sha256). They MUST be sent
// verbatim because they are folded into the SigV4 signed-headers list;
// omitting or altering them causes MinIO to reject the upload with
// SignatureDoesNotMatch / XAmzContentSHA256Mismatch.
func uploadViaPresignedURL(t *testing.T, presignedURL, contentType string, payload []byte, requiredHeaders map[string]string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(payload))
	require.NoError(t, err, "build PUT request")
	req.Header.Set("Content-Type", contentType)
	for k, v := range requiredHeaders {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "PUT to presigned URL")
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	require.GreaterOrEqualf(t, resp.StatusCode, 200, "PUT %s -> %d body=%s", presignedURL, resp.StatusCode, string(raw))
	require.Lessf(t, resp.StatusCode, 300, "PUT %s -> %d body=%s", presignedURL, resp.StatusCode, string(raw))
}

// putToPresignedURLStatus is the negative-path counterpart to
// uploadViaPresignedURL: it issues the PUT and returns the status code
// + raw body without asserting success, so the caller can verify that
// MinIO rejects mismatched / missing signed headers.
func putToPresignedURLStatus(t *testing.T, presignedURL, contentType string, payload []byte, requiredHeaders map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(payload))
	require.NoError(t, err, "build PUT request")
	req.Header.Set("Content-Type", contentType)
	for k, v := range requiredHeaders {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "PUT to presigned URL")
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// createTaskForAttachment creates a task in the tenant's default
// project and returns its public id. Centralised so the dedup tests
// stay focused on attachment behaviour rather than task plumbing.
func createTaskForAttachment(t *testing.T, token, projectID, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", token, map[string]any{
		"projectId": projectID,
		"title":     title,
	}, &task)
	require.NotEmpty(t, task.ID, "task create returned empty id")
	return task.ID
}

// storageObjectRefCount returns the storage_objects.ref_count for the
// row with the supplied storage_key, or (0, false) if the row is gone.
// Direct SQL is allowed here because the API does not expose
// storage_objects internals.
func storageObjectRefCount(t *testing.T, db *sql.DB, storageKey string) (uint32, bool) {
	t.Helper()
	var rc uint32
	err := db.QueryRowContext(context.Background(),
		`SELECT ref_count FROM storage_objects WHERE storage_key = ? LIMIT 1`, storageKey).Scan(&rc)
	if err == sql.ErrNoRows {
		return 0, false
	}
	require.NoError(t, err)
	return rc, true
}

// countAttachmentsForStorageKey returns the number of attachments rows
// pointing at the storage_objects row identified by storage_key.
func countAttachmentsForStorageKey(t *testing.T, db *sql.DB, storageKey string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM attachments a
		   JOIN storage_objects so ON so.id = a.storage_object_id
		  WHERE so.storage_key = ?`, storageKey).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestPresignDedupHit verifies that re-uploading the same payload in
// the same workspace produces a single storage_objects row whose
// ref_count tracks the number of attachments pointing at it.
func TestPresignDedupHit(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "Dedup hit")
	payload := makePNG(t, 8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	t.Run("first presign uploads to MinIO", func(t *testing.T) {
		first := presignAttachment(t, tt.AccessToken, taskID, "doc.png", "image/png", payload)
		require.False(t, first.Deduplicated, "first upload must not be deduplicated")
		require.NotEmpty(t, first.UploadURL, "first upload must return a presigned PUT URL")
		require.NotEmpty(t, first.StorageKey)
		require.NotEmpty(t, first.AttachmentID)

		require.NotNil(t, first.RequiredHeaders, "miss branch must return requiredHeaders")
		expectedSHA := sha256.Sum256(payload)
		require.Equal(t, hex.EncodeToString(expectedSHA[:]),
			first.RequiredHeaders["x-amz-content-sha256"],
			"requiredHeaders must pin the body hash for SigV4 verification")
		uploadViaPresignedURL(t, first.UploadURL, "image/png", payload, first.RequiredHeaders)
		testStorage.MustExist(t, first.StorageKey)

		rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
		require.True(t, ok, "storage_objects row must exist after first upload")
		require.Equal(t, uint32(1), rc, "ref_count after first upload")
		require.Equal(t, 1, countAttachmentsForStorageKey(t, testDB, first.StorageKey))

		t.Run("second presign hits dedup", func(t *testing.T) {
			second := presignAttachment(t, tt.AccessToken, taskID, "doc-copy.png", "image/png", payload)
			require.True(t, second.Deduplicated, "second upload must be deduplicated")
			require.Empty(t, second.UploadURL, "deduplicated response must NOT carry a presigned PUT URL")
			require.Empty(t, second.RequiredHeaders, "deduplicated response must NOT carry requiredHeaders (no PUT happens)")
			require.Equal(t, first.StorageKey, second.StorageKey, "dedup must reuse the same storage key")
			require.NotEqual(t, first.AttachmentID, second.AttachmentID, "each presign creates a distinct attachment row")

			rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
			require.True(t, ok)
			require.Equal(t, uint32(2), rc, "ref_count must increment on dedup hit")
			require.Equal(t, 2, countAttachmentsForStorageKey(t, testDB, first.StorageKey),
				"both presigns must produce attachment rows pointing at the same storage_object")
		})
	})
}

// TestPresignDifferentWorkspaces asserts that the (workspace_id, sha256)
// UNIQUE key keeps dedup workspace-scoped: two tenants uploading the
// same bytes get two distinct storage_objects rows so neither tenant
// can observe the other's data via shared keys.
func TestPresignDifferentWorkspaces(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	a := newTenant(t)
	b := newTenant(t)
	require.NotEqual(t, a.WorkspacePublicID, b.WorkspacePublicID,
		"tenants must own distinct workspaces")

	taskA := createTaskForAttachment(t, a.AccessToken, a.ProjectPublicID, "A")
	taskB := createTaskForAttachment(t, b.AccessToken, b.ProjectPublicID, "B")

	payload := makePNG(t, 8, 8, color.RGBA{R: 9, G: 9, B: 9, A: 255})

	resA := presignAttachment(t, a.AccessToken, taskA, "shared.png", "image/png", payload)
	require.False(t, resA.Deduplicated, "tenant A first upload must not be deduplicated")
	uploadViaPresignedURL(t, resA.UploadURL, "image/png", payload, resA.RequiredHeaders)

	resB := presignAttachment(t, b.AccessToken, taskB, "shared.png", "image/png", payload)
	require.False(t, resB.Deduplicated, "tenant B first upload must not be deduplicated even with identical sha256")
	require.NotEmpty(t, resB.UploadURL)
	require.NotEqual(t, resA.StorageKey, resB.StorageKey,
		"workspace-scoped dedup must produce distinct storage keys across tenants")
	uploadViaPresignedURL(t, resB.UploadURL, "image/png", payload, resB.RequiredHeaders)

	rcA, okA := storageObjectRefCount(t, testDB, resA.StorageKey)
	rcB, okB := storageObjectRefCount(t, testDB, resB.StorageKey)
	require.True(t, okA && okB, "both workspace-scoped storage_objects rows must exist")
	require.Equal(t, uint32(1), rcA, "tenant A ref_count")
	require.Equal(t, uint32(1), rcB, "tenant B ref_count")
}

// TestDeleteRefCountGc walks the full delete lifecycle for a deduped
// pair: the first delete decrements ref_count and leaves the blob in
// place, the second delete drops ref_count to 0 which removes both the
// storage_objects row AND the underlying MinIO object.
func TestDeleteRefCountGc(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "GC test")
	payload := makePNG(t, 8, 8, color.RGBA{R: 200, G: 50, B: 50, A: 255})

	first := presignAttachment(t, tt.AccessToken, taskID, "first.png", "image/png", payload)
	require.False(t, first.Deduplicated)
	uploadViaPresignedURL(t, first.UploadURL, "image/png", payload, first.RequiredHeaders)

	second := presignAttachment(t, tt.AccessToken, taskID, "second.png", "image/png", payload)
	require.True(t, second.Deduplicated, "second presign must dedup")

	rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
	require.True(t, ok)
	require.Equal(t, uint32(2), rc, "ref_count starts at 2 (one per attachment)")
	testStorage.MustExist(t, first.StorageKey)

	t.Run("delete first attachment: ref_count drops, blob survives", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskID, first.AttachmentID),
			tt.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "delete first attachment body=%s", string(body))

		rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
		require.True(t, ok, "storage_objects row must remain after first delete")
		require.Equal(t, uint32(1), rc, "ref_count after one delete")
		testStorage.MustExist(t, first.StorageKey)
	})

	t.Run("delete second attachment: GC runs, blob removed", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/tasks/%s/attachments/%s", testServerURL, taskID, second.AttachmentID),
			tt.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "delete second attachment body=%s", string(body))

		_, ok := storageObjectRefCount(t, testDB, first.StorageKey)
		require.False(t, ok, "storage_objects row must be deleted when ref_count hits 0")
		testStorage.MustNotExist(t, first.StorageKey)
	})
}

// TestJapaneseFilename verifies non-ASCII filenames survive the round
// trip end-to-end:
//   - Stored in attachments.filename as UTF-8 (no transcoding)
//   - Surfaced unchanged on the list endpoint
//   - Embedded in the presigned download URL using the RFC 5987
//     percent-encoded UTF-8 form so browsers render the original glyphs
func TestJapaneseFilename(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "i18n filename")

	payload := makePNG(t, 12, 12, color.RGBA{R: 0, G: 128, B: 255, A: 255})
	original := "日本語_テスト.png"
	const expectedRFC5987 = "%E6%97%A5%E6%9C%AC%E8%AA%9E_%E3%83%86%E3%82%B9%E3%83%88.png"

	res := presignAttachment(t, tt.AccessToken, taskID, original, "image/png", payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

	t.Run("list returns UTF-8 filename verbatim", func(t *testing.T) {
		var list struct {
			Attachments []struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments", testServerURL, taskID),
			tt.AccessToken, nil, &list)
		require.Len(t, list.Attachments, 1)
		require.Equal(t, original, list.Attachments[0].Filename,
			"filename column must preserve UTF-8 across persistence + JSON marshalling")
	})

	t.Run("download URL embeds RFC 5987 encoded filename", func(t *testing.T) {
		var dl struct {
			DownloadURL string `json:"downloadUrl"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments/%s/download", testServerURL, taskID, res.AttachmentID),
			tt.AccessToken, nil, &dl)
		require.NotEmpty(t, dl.DownloadURL)

		parsed, err := url.Parse(dl.DownloadURL)
		require.NoError(t, err)
		disp := parsed.Query().Get("response-content-disposition")
		require.NotEmpty(t, disp,
			"presigned URL must carry response-content-disposition for browser download")
		require.True(t, strings.Contains(disp, "filename*=UTF-8''"+expectedRFC5987),
			"Content-Disposition param must use RFC 5987 percent-encoded UTF-8: got %q", disp)
	})

	t.Run("GET against presigned URL returns RFC 5987 Content-Disposition header", func(t *testing.T) {
		var dl struct {
			DownloadURL string `json:"downloadUrl"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/tasks/%s/attachments/%s/download", testServerURL, taskID, res.AttachmentID),
			tt.AccessToken, nil, &dl)

		resp, err := http.Get(dl.DownloadURL) //nolint:gosec,noctx // testing presigned URL flow
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)

		require.Equal(t, http.StatusOK, resp.StatusCode,
			"GET against presigned URL must return 200")
		disp := resp.Header.Get("Content-Disposition")
		require.True(t, strings.Contains(disp, "filename*=UTF-8''"+expectedRFC5987),
			"Content-Disposition response header must round-trip the RFC 5987 form: got %q", disp)
	})
}
