// Calendar event-attachment parity suite. The task-attachment dedup
// pipeline got its full security audit in attachment_dedup_test.go +
// attachment_security_test.go; this file mirrors the load-bearing
// invariants for the calendar event surface so the second presign
// endpoint cannot drift behind:
//
//   - dedup hit on identical bytes within a workspace
//   - ref-count GC dropping the MinIO blob on last delete
//   - non-ASCII filenames round-trip via RFC 5987
//   - SigV4 hash-mismatch rejection
//
// Calendar attachments live behind /workspaces/{wsId}/calendars/{calId}
// /events/{evtId}/attachments/* and are mediated by
// internal/http/handlers/calendars/attachments.go, which shares the
// storage_objects table and dedup machinery with the task surface.
package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// calendarPresignResponse is the calendar-side analogue of
// presignResponse. The wire shape matches because both endpoints
// implement PresignUploadOutputBody / PresignAttachmentOutput.Body
// from the same set of conventions.
type calendarPresignResponse struct {
	UploadURL       string            `json:"uploadUrl"`
	StorageKey      string            `json:"storageKey"`
	AttachmentID    string            `json:"attachmentId"`
	Deduplicated    bool              `json:"deduplicated"`
	RequiredHeaders map[string]string `json:"requiredHeaders"`
}

// createCalendarForAttachments creates a personal calendar in the
// tenant's workspace and returns its public id.
func createCalendarForAttachments(t *testing.T, tt persona) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.workspaceID()+"/calendars",
		tt.token(), map[string]any{
			"kind":  "personal",
			"name":  "Attachments Cal " + randomHex(4),
			"color": "#4285F4",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create calendar must return public id")
	return resp.ID
}

// createCalendarEventForAttachments creates a one-hour event on the
// supplied calendar so the attachment endpoints have a parent row to
// hang off of.
func createCalendarEventForAttachments(t *testing.T, tt persona, calID, title string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	start := time.Date(2027, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events",
			testServerURL, tt.workspaceID(), calID),
		tt.token(), map[string]any{
			"kind":     "event",
			"title":    title,
			"startAt":  start.Unix(),
			"endAt":    end.Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return public id")
	return resp.ID
}

// presignCalendarAttachment hits POST .../events/{evtId}/attachments
// /presign and decodes the response.
func presignCalendarAttachment(
	t *testing.T,
	tt persona,
	calID, evtID, filename, contentType string,
	payload []byte,
) calendarPresignResponse {
	t.Helper()
	sum := sha256.Sum256(payload)
	body := map[string]any{
		"filename":    filename,
		"contentType": contentType,
		"byteSize":    len(payload),
		"sha256":      hex.EncodeToString(sum[:]),
	}
	var out calendarPresignResponse
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/presign",
			testServerURL, tt.workspaceID(), calID, evtID),
		tt.token(), body, &out)
	return out
}

// confirmCalendarAttachment completes a calendar upload the way a real
// client does, mirroring confirmAttachment on the task surface: the
// presign only reserves a row, and the confirm is what measures the
// object and turns the declared size into a checked one. Skipping it
// leaves a reservation, which is deliberately not a dedup candidate.
func confirmCalendarAttachment(t *testing.T, tt persona, calID, evtID, attID string) {
	t.Helper()
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s/confirm",
			testServerURL, tt.workspaceID(), calID, evtID, attID),
		tt.token(), nil, nil)
}

// persona is the smallest interface attachment helpers need: a token
// and a workspace public id. Both *helpers.TestTenant and any future
// personas can satisfy it via the methods below.
type persona interface {
	token() string
	workspaceID() string
}

// tenantPersona adapts *helpers.TestTenant to persona without forcing
// helpers.TestTenant to grow methods just for tests.
type tenantPersona struct {
	tok string
	ws  string
}

func (p tenantPersona) token() string       { return p.tok }
func (p tenantPersona) workspaceID() string { return p.ws }

// asPersona is the conversion shim from a fresh tenant to persona.
func asPersona(t *testing.T) persona {
	t.Helper()
	tt := newTenant(t)
	return tenantPersona{tok: tt.AccessToken, ws: tt.WorkspacePublicID}
}

// TestCalendarPresignDedupHit mirrors TestPresignDedupHit for the
// calendar event surface. Two presigns for the same bytes in the same
// workspace produce a single storage_objects row and two attachments
// rows; ref_count tracks the count.
func TestCalendarPresignDedupHit(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := asPersona(t)
	calID := createCalendarForAttachments(t, tt)
	evtID := createCalendarEventForAttachments(t, tt, calID, "Cal Dedup")

	payload := makePNG(t, 8, 8, color.RGBA{R: 41, G: 42, B: 43, A: 255})
	first := presignCalendarAttachment(t, tt, calID, evtID, "doc.png", "image/png", payload)
	require.False(t, first.Deduplicated, "first calendar upload must not be deduplicated")
	require.NotEmpty(t, first.UploadURL)
	require.NotEmpty(t, first.RequiredHeaders["x-amz-content-sha256"],
		"miss branch must return signed-header binding")
	uploadViaPresignedURL(t, first.UploadURL, "image/png", payload, first.RequiredHeaders)
	confirmCalendarAttachment(t, tt, calID, evtID, first.AttachmentID)
	testStorage.MustExist(t, first.StorageKey)

	rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
	require.True(t, ok)
	require.Equal(t, uint32(1), rc, "ref_count after first calendar upload")

	second := presignCalendarAttachment(t, tt, calID, evtID, "doc-copy.png", "image/png", payload)
	require.True(t, second.Deduplicated, "second calendar upload must dedup")
	require.Empty(t, second.UploadURL, "deduped response must NOT return a presigned PUT URL")
	require.Empty(t, second.RequiredHeaders, "deduped response must NOT return signed-header bindings")
	require.Equal(t, first.StorageKey, second.StorageKey,
		"calendar dedup must reuse the same storage_objects.storage_key")
	require.NotEqual(t, first.AttachmentID, second.AttachmentID,
		"each calendar presign creates a distinct attachment row")

	rc2, ok := storageObjectRefCount(t, testDB, first.StorageKey)
	require.True(t, ok)
	require.Equal(t, uint32(2), rc2, "ref_count must increment on calendar dedup hit")
	require.Equal(t, 2, countCalendarEventAttachmentsForStorageKey(t, testDB, first.StorageKey),
		"both presigns must produce calendar_event_attachments rows pointing at the same blob")
}

// TestCalendarDeleteRefCountGc mirrors TestDeleteRefCountGc for the
// calendar event surface. Each delete drops ref_count by one; when
// the last reference goes the storage_objects row + the MinIO blob
// disappear.
func TestCalendarDeleteRefCountGc(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := asPersona(t)
	calID := createCalendarForAttachments(t, tt)
	evtID := createCalendarEventForAttachments(t, tt, calID, "Cal GC")

	payload := makePNG(t, 8, 8, color.RGBA{R: 9, G: 80, B: 70, A: 255})
	first := presignCalendarAttachment(t, tt, calID, evtID, "first.png", "image/png", payload)
	require.False(t, first.Deduplicated)
	uploadViaPresignedURL(t, first.UploadURL, "image/png", payload, first.RequiredHeaders)
	confirmCalendarAttachment(t, tt, calID, evtID, first.AttachmentID)

	second := presignCalendarAttachment(t, tt, calID, evtID, "second.png", "image/png", payload)
	require.True(t, second.Deduplicated)

	rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
	require.True(t, ok)
	require.Equal(t, uint32(2), rc, "calendar ref_count starts at 2")
	testStorage.MustExist(t, first.StorageKey)

	t.Run("first delete: ref_count drops, blob survives", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s",
				testServerURL, tt.workspaceID(), calID, evtID, first.AttachmentID),
			tt.token(), nil)
		require.Equalf(t, http.StatusOK, status,
			"calendar attachment delete must succeed; body=%s", string(body))
		rc, ok := storageObjectRefCount(t, testDB, first.StorageKey)
		require.True(t, ok, "calendar storage_objects row must remain after first delete")
		require.Equal(t, uint32(1), rc)
		testStorage.MustExist(t, first.StorageKey)
	})

	t.Run("second delete: GC fires, blob removed", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodDelete,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s",
				testServerURL, tt.workspaceID(), calID, evtID, second.AttachmentID),
			tt.token(), nil)
		require.Equalf(t, http.StatusOK, status,
			"calendar attachment delete must succeed; body=%s", string(body))
		_, ok := storageObjectRefCount(t, testDB, first.StorageKey)
		require.False(t, ok, "calendar storage_objects row must be deleted at ref_count=0")
		testStorage.MustNotExist(t, first.StorageKey)
	})
}

// TestCalendarJapaneseFilename mirrors TestJapaneseFilename for the
// calendar event surface. Filename + RFC 5987 download header parity
// is the bug class we are guarding against.
func TestCalendarJapaneseFilename(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := asPersona(t)
	calID := createCalendarForAttachments(t, tt)
	evtID := createCalendarEventForAttachments(t, tt, calID, "Cal i18n")

	payload := makePNG(t, 12, 12, color.RGBA{R: 0, G: 200, B: 100, A: 255})
	original := "予定_資料.png"
	const expectedRFC5987 = "%E4%BA%88%E5%AE%9A_%E8%B3%87%E6%96%99.png"

	res := presignCalendarAttachment(t, tt, calID, evtID, original, "image/png", payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, "image/png", payload, res.RequiredHeaders)

	t.Run("calendar list returns UTF-8 filename verbatim", func(t *testing.T) {
		var list struct {
			Attachments []struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments",
				testServerURL, tt.workspaceID(), calID, evtID),
			tt.token(), nil, &list)
		require.Len(t, list.Attachments, 1)
		require.Equal(t, original, list.Attachments[0].Filename,
			"calendar filename column must preserve UTF-8 across persistence + JSON")
	})

	t.Run("calendar download URL embeds RFC 5987 percent-encoded filename", func(t *testing.T) {
		var dl struct {
			DownloadURL string `json:"downloadUrl"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s/download",
				testServerURL, tt.workspaceID(), calID, evtID, res.AttachmentID),
			tt.token(), nil, &dl)
		require.NotEmpty(t, dl.DownloadURL)

		parsed, err := url.Parse(dl.DownloadURL)
		require.NoError(t, err)
		disp := parsed.Query().Get("response-content-disposition")
		require.NotEmpty(t, disp,
			"calendar presigned URL must carry response-content-disposition")
		require.Truef(t, strings.Contains(disp, "filename*=UTF-8''"+expectedRFC5987),
			"calendar Content-Disposition param must be RFC 5987: got %q", disp)
	})

	t.Run("calendar GET against presigned URL surfaces RFC 5987 header", func(t *testing.T) {
		var dl struct {
			DownloadURL string `json:"downloadUrl"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s/download",
				testServerURL, tt.workspaceID(), calID, evtID, res.AttachmentID),
			tt.token(), nil, &dl)

		resp, err := http.Get(dl.DownloadURL) //nolint:gosec,noctx // testing presigned URL flow
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		disp := resp.Header.Get("Content-Disposition")
		require.Truef(t, strings.Contains(disp, "filename*=UTF-8''"+expectedRFC5987),
			"calendar Content-Disposition response header must round-trip RFC 5987: got %q", disp)
	})
}

// TestCalendarHashTamperRejected mirrors TestPresignHashTamperRejected
// for the calendar event surface. The shared SigV4 binding via
// PresignPutWithSha256 rejects the upload when bytes do not match the
// declared hash.
func TestCalendarHashTamperRejected(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := asPersona(t)
	calID := createCalendarForAttachments(t, tt)
	evtID := createCalendarEventForAttachments(t, tt, calID, "Cal Tamper")

	declared := makePNG(t, 8, 8, color.RGBA{R: 11, G: 22, B: 33, A: 255})
	tampered := makePNG(t, 8, 8, color.RGBA{R: 99, G: 99, B: 99, A: 255})

	res := presignCalendarAttachment(t, tt, calID, evtID, "innocent.png", "image/png", declared)
	require.False(t, res.Deduplicated)
	require.NotEmpty(t, res.UploadURL)

	status, raw := putToPresignedURLStatus(t, res.UploadURL, "image/png", tampered, res.RequiredHeaders)
	require.GreaterOrEqualf(t, status, 400, "MinIO must reject; body=%s", string(raw))
	require.Lessf(t, status, 500, "rejection must be 4xx; body=%s", string(raw))
	testStorage.MustNotExist(t, res.StorageKey)
}

// countCalendarEventAttachmentsForStorageKey counts how many
// calendar_event_attachments rows reference the storage_objects row
// identified by storage_key. The counterpart to
// countAttachmentsForStorageKey for the calendar surface; both
// surfaces share storage_objects but have separate attachments tables.
func countCalendarEventAttachmentsForStorageKey(t *testing.T, db *sql.DB, storageKey string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_event_attachments cea
		   JOIN storage_objects so ON so.id = cea.storage_object_id
		  WHERE so.storage_key = ?`, storageKey).Scan(&n)
	require.NoError(t, err)
	return n
}
