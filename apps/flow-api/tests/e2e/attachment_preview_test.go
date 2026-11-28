// Attachment preview suite: the task and calendar-event preview endpoints,
// the previewable flag on both lists, and the audit behaviour that differs
// between the two surfaces. Uploads go through presign, the signed relay PUT
// and confirm, the same way a client does.
package e2e

import (
	"fmt"
	"image/color"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	flowstorage "github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

const (
	apSandboxCSP     = "default-src 'none'; sandbox; frame-ancestors 'none'"
	apNotPreviewable = "VALIDATION.FILE.NOT_PREVIEWABLE"
)

type apPreviewBody struct {
	PreviewURL string `json:"previewUrl"`
	ExpiresAt  int64  `json:"expiresAt"`
}

type apListedAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Previewable bool   `json:"previewable"`
}

// apFile is one attachment the tests uploaded: what was declared and what
// the API handed back.
type apFile struct {
	id, storageKey, filename, contentType string
	payload                               []byte
}

// apBytes returns distinct, non-image-looking bytes so a test never depends
// on content sniffing and no two files in a workspace dedup into one blob.
func apBytes(t *testing.T, tag string) []byte {
	t.Helper()
	return append([]byte(tag+":"), randomBytes(t, 48)...)
}

func apUploadTask(t *testing.T, tt *helpers.TestTenant, taskID, filename, contentType string, payload []byte) apFile {
	t.Helper()
	res := presignAttachment(t, tt.AccessToken, taskID, filename, contentType, payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, contentType, payload)
	confirmAttachment(t, tt.AccessToken, taskID, res.AttachmentID)
	return apFile{res.AttachmentID, res.StorageKey, filename, contentType, payload}
}

func apUploadCalendar(t *testing.T, p persona, calID, evtID, filename, contentType string, payload []byte) apFile {
	t.Helper()
	res := presignCalendarAttachment(t, p, calID, evtID, filename, contentType, payload)
	require.False(t, res.Deduplicated)
	uploadViaPresignedURL(t, res.UploadURL, contentType, payload)
	confirmCalendarAttachment(t, p, calID, evtID, res.AttachmentID)
	return apFile{res.AttachmentID, res.StorageKey, filename, contentType, payload}
}

func apTaskPreviewPath(taskID, attID, leaf string) string {
	return fmt.Sprintf("%s/tasks/%s/attachments/%s/%s", testServerURL, taskID, attID, leaf)
}

func apCalPreviewPath(p persona, calID, evtID, attID, leaf string) string {
	return fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments/%s/%s",
		testServerURL, p.workspaceID(), calID, evtID, attID, leaf)
}

// apRequireServedInline fetches a preview URL and checks the response is the
// stored bytes under the allowlisted type with the sandbox headers.
func apRequireServedInline(t *testing.T, body apPreviewBody, f apFile, wantType string) {
	t.Helper()
	require.NotEmpty(t, body.PreviewURL)
	require.Contains(t, body.PreviewURL, "/blobs/"+f.storageKey)
	now := time.Now()
	require.Greater(t, body.ExpiresAt, now.Add(10*time.Minute).Unix(), "valid for at least 15 minutes")
	require.LessOrEqual(t, body.ExpiresAt, now.Add(31*time.Minute).Unix(), "valid for at most 30 minutes")

	resp, err := http.Get(body.PreviewURL) //nolint:gosec,noctx // signed relay URL under test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, f.payload, raw)
	require.Equal(t, wantType, resp.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, apSandboxCSP, resp.Header.Get("Content-Security-Policy"))
	require.Equal(t, flowstorage.ContentDispositionInline(f.filename), resp.Header.Get("Content-Disposition"))
}

// apSettledAuditCount counts audit rows for an action in a workspace once the
// asynchronous writer has gone quiet. A resource of "" counts the action
// across the workspace.
func apSettledAuditCount(t *testing.T, wsPublicID, action, resourcePublicID string) int {
	t.Helper()
	count := func() int {
		var n int
		q := `SELECT COUNT(*) FROM audit_logs
		       WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		         AND action = ?`
		args := []any{wsPublicID, action}
		if resourcePublicID != "" {
			q += ` AND resource_public_id = UUID_TO_BIN(?, 0)`
			args = append(args, resourcePublicID)
		}
		require.NoError(t, testDB.QueryRow(q, args...).Scan(&n))
		return n
	}
	last := -1
	for i := 0; i < 40; i++ {
		n := count()
		if n == last {
			return n
		}
		last = n
		time.Sleep(150 * time.Millisecond)
	}
	require.FailNow(t, "audit log did not settle")
	return 0
}

// apRequireSameRefusal checks that the preview route refuses a request with
// the status and code the download route gives for it, and that both match
// the expected pair.
func apRequireSameRefusal(t *testing.T, token, previewURL, downloadURL string, wantStatus int, wantCode, label string) {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodGet, previewURL, token, nil)
	requireDenied(t, status, body, wantStatus, wantCode, label+" (preview)")
	status, body = doJSONStatus(t, http.MethodGet, downloadURL, token, nil)
	requireDenied(t, status, body, wantStatus, wantCode, label+" (download)")
}

func TestAttachmentPreviewTaskServesImages(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "preview types")

	cases := []struct{ declared, served, name string }{
		{"image/png", "image/png", "a.png"},
		{"image/jpeg", "image/jpeg", "b.jpg"},
		{"image/gif", "image/gif", "c.gif"},
		{"image/webp", "image/webp", "d.webp"},
		{"image/avif", "image/avif", "e.avif"},
		{"IMAGE/PNG; charset=binary", "image/png", "f.png"},
		{"image/jpg", "image/jpeg", "g.jpg"},
		{"image/pjpeg", "image/jpeg", "h.jpg"},
		{"image/x-png", "image/png", "i.png"},
		{"image/png", "image/png", "日本語 写真.png"},
	}
	for _, c := range cases {
		t.Run(c.declared+"/"+c.name, func(t *testing.T) {
			t.Parallel()
			payload := apBytes(t, c.name)
			if c.declared == "image/png" && c.name == "a.png" {
				payload = makePNG(t, 8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
			}
			f := apUploadTask(t, tt, taskID, c.name, c.declared, payload)

			var out apPreviewBody
			doJSON(t, http.MethodGet, apTaskPreviewPath(taskID, f.id, "preview"), tt.AccessToken, nil, &out)
			apRequireServedInline(t, out, f, c.served)
		})
	}
}

func TestAttachmentPreviewTaskListFlagAndNotPreviewable(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "preview flag")

	png := apUploadTask(t, tt, taskID, "ok.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 5, G: 6, B: 7, A: 255}))
	svg := apUploadTask(t, tt, taskID, "vector.svg", "image/svg+xml",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	pdf := apUploadTask(t, tt, taskID, "doc.pdf", "application/pdf", []byte("%PDF-1.4\n%%EOF\n"))
	html := apUploadTask(t, tt, taskID, "page.html", "text/html", []byte("<html><script>1</script></html>"))
	// The extension says image, the declared type does not: the type decides.
	mislabelled := apUploadTask(t, tt, taskID, "looks-like.png", "application/pdf", []byte("%PDF-1.4\n%%EOF\n%x"))

	t.Run("list carries previewable per row", func(t *testing.T) {
		var list struct {
			Attachments []apListedAttachment `json:"attachments"`
		}
		doJSON(t, http.MethodGet, fmt.Sprintf("%s/tasks/%s/attachments", testServerURL, taskID), tt.AccessToken, nil, &list)
		got := map[string]bool{}
		for _, a := range list.Attachments {
			got[a.ID] = a.Previewable
		}
		require.Equal(t, map[string]bool{
			png.id: true, svg.id: false, pdf.id: false, html.id: false, mislabelled.id: false,
		}, got)
	})

	t.Run("image is previewable", func(t *testing.T) {
		var out apPreviewBody
		doJSON(t, http.MethodGet, apTaskPreviewPath(taskID, png.id, "preview"), tt.AccessToken, nil, &out)
		apRequireServedInline(t, out, png, "image/png")
	})

	for name, f := range map[string]apFile{"svg": svg, "pdf": pdf, "html": html, "pdf named .png": mislabelled} {
		t.Run(name+" is refused with 415", func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodGet, apTaskPreviewPath(taskID, f.id, "preview"), tt.AccessToken, nil)
			requireDenied(t, status, body, http.StatusUnsupportedMediaType, apNotPreviewable, name)

			// Download still works for the same row.
			var dl struct {
				DownloadURL string `json:"downloadUrl"`
			}
			doJSON(t, http.MethodGet, apTaskPreviewPath(taskID, f.id, "download"), tt.AccessToken, nil, &dl)
			require.NotEmpty(t, dl.DownloadURL)
		})
	}
}

func TestAttachmentPreviewTaskRefusals(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	outsider := newTenant(t)
	taskA := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "preview owner")
	taskB := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "preview sibling")
	f := apUploadTask(t, tt, taskA, "mine.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 15, G: 16, B: 17, A: 255}))

	t.Run("owning task succeeds", func(t *testing.T) {
		var out apPreviewBody
		doJSON(t, http.MethodGet, apTaskPreviewPath(taskA, f.id, "preview"), tt.AccessToken, nil, &out)
		apRequireServedInline(t, out, f, "image/png")
	})

	t.Run("sibling task", func(t *testing.T) {
		apRequireSameRefusal(t, tt.AccessToken,
			apTaskPreviewPath(taskB, f.id, "preview"), apTaskPreviewPath(taskB, f.id, "download"),
			http.StatusNotFound, "WS.TASK.NOT_FOUND", "attachment of another task")
	})

	t.Run("unknown attachment id", func(t *testing.T) {
		missing := uuid.Must(uuid.NewV7()).String()
		apRequireSameRefusal(t, tt.AccessToken,
			apTaskPreviewPath(taskA, missing, "preview"), apTaskPreviewPath(taskA, missing, "download"),
			http.StatusNotFound, "WS.TASK.NOT_FOUND", "unknown attachment id")
	})

	t.Run("attachment id that is not a uuid", func(t *testing.T) {
		apRequireSameRefusal(t, tt.AccessToken,
			apTaskPreviewPath(taskA, "not-a-uuid", "preview"), apTaskPreviewPath(taskA, "not-a-uuid", "download"),
			http.StatusBadRequest, "VALIDATION.PATH.PARAM_INVALID", "malformed attachment id")
	})

	t.Run("other tenant", func(t *testing.T) {
		apRequireSameRefusal(t, outsider.AccessToken,
			apTaskPreviewPath(taskA, f.id, "preview"), apTaskPreviewPath(taskA, f.id, "download"),
			http.StatusForbidden, "WS.TASK.ACCESS_DENIED", "attachment of another tenant's task")
	})

	t.Run("no token", func(t *testing.T) {
		status, _ := doJSONStatus(t, http.MethodGet, apTaskPreviewPath(taskA, f.id, "preview"), "", nil)
		require.Equal(t, http.StatusUnauthorized, status)
	})
}

// A task preview, like a task download, writes no audit row.
func TestAttachmentPreviewTaskWritesNoAudit(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAttachment(t, tt.AccessToken, tt.ProjectPublicID, "preview audit")
	f := apUploadTask(t, tt, taskID, "a.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 25, G: 26, B: 27, A: 255}))

	countAll := func() int {
		var n int
		require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM audit_logs
			WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
			tt.WorkspacePublicID).Scan(&n))
		return n
	}
	// Let the writes from setup land before taking the baseline.
	for last, i := -1, 0; i < 40 && last != countAll(); i++ {
		last = countAll()
		time.Sleep(150 * time.Millisecond)
	}
	before := countAll()

	var out apPreviewBody
	doJSON(t, http.MethodGet, apTaskPreviewPath(taskID, f.id, "preview"), tt.AccessToken, nil, &out)
	doJSON(t, http.MethodGet, apTaskPreviewPath(taskID, f.id, "preview"), tt.AccessToken, nil, &out)

	time.Sleep(600 * time.Millisecond)
	require.Equal(t, before, countAll(), "a task preview records no audit row")
	require.Zero(t, apSettledAuditCount(t, tt.WorkspacePublicID, "calendar.attachment.preview", ""))
}

func TestAttachmentPreviewCalendarServesImages(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	p := asPersona(t)
	calID := createCalendarForAttachments(t, p)
	evtID := createCalendarEventForAttachments(t, p, calID, "preview types")

	cases := []struct{ declared, served, name string }{
		{"image/png", "image/png", "a.png"},
		{"image/jpeg", "image/jpeg", "b.jpg"},
		{"image/gif", "image/gif", "c.gif"},
		{"image/webp", "image/webp", "d.webp"},
		{"image/avif", "image/avif", "e.avif"},
		{"image/jpg", "image/jpeg", "g.jpg"},
		{"image/png", "image/png", "予定 写真.png"},
	}
	for _, c := range cases {
		t.Run(c.declared+"/"+c.name, func(t *testing.T) {
			t.Parallel()
			f := apUploadCalendar(t, p, calID, evtID, c.name, c.declared, apBytes(t, c.name))

			var out apPreviewBody
			doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, f.id, "preview"), p.token(), nil, &out)
			apRequireServedInline(t, out, f, c.served)
		})
	}
}

func TestAttachmentPreviewCalendarListFlagAndNotPreviewable(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	p := asPersona(t)
	calID := createCalendarForAttachments(t, p)
	evtID := createCalendarEventForAttachments(t, p, calID, "preview flag")

	png := apUploadCalendar(t, p, calID, evtID, "ok.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 35, G: 36, B: 37, A: 255}))
	svg := apUploadCalendar(t, p, calID, evtID, "vector.svg", "image/svg+xml",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	pdf := apUploadCalendar(t, p, calID, evtID, "doc.pdf", "application/pdf", []byte("%PDF-1.4\n%%EOF\n"))
	mislabelled := apUploadCalendar(t, p, calID, evtID, "looks-like.png", "application/pdf", []byte("%PDF-1.4\n%%EOF\n%y"))

	t.Run("list carries previewable per row", func(t *testing.T) {
		var list struct {
			Attachments []apListedAttachment `json:"attachments"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s/attachments", testServerURL, p.workspaceID(), calID, evtID),
			p.token(), nil, &list)
		got := map[string]bool{}
		for _, a := range list.Attachments {
			got[a.ID] = a.Previewable
		}
		require.Equal(t, map[string]bool{png.id: true, svg.id: false, pdf.id: false, mislabelled.id: false}, got)
	})

	t.Run("image is previewable", func(t *testing.T) {
		var out apPreviewBody
		doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, png.id, "preview"), p.token(), nil, &out)
		apRequireServedInline(t, out, png, "image/png")
	})

	for name, f := range map[string]apFile{"svg": svg, "pdf": pdf, "pdf named .png": mislabelled} {
		t.Run(name+" is refused with 415", func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, f.id, "preview"), p.token(), nil)
			requireDenied(t, status, body, http.StatusUnsupportedMediaType, apNotPreviewable, name)
		})
	}
}

func TestAttachmentPreviewCalendarRefusals(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	host := newTenant(t)
	p := tenantPersona{tok: host.AccessToken, ws: host.WorkspacePublicID}
	outsider := newTenant(t)
	otherWs := tenantPersona{tok: outsider.AccessToken, ws: outsider.WorkspacePublicID}

	calID := createCalendarForAttachments(t, p)
	evtA := createCalendarEventForAttachments(t, p, calID, "preview event A")
	evtB := createCalendarEventForAttachments(t, p, calID, "preview event B")
	otherCal := createCalendarForAttachments(t, p)
	f := apUploadCalendar(t, p, calID, evtA, "mine.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 45, G: 46, B: 47, A: 255}))

	t.Run("owning event succeeds", func(t *testing.T) {
		var out apPreviewBody
		doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtA, f.id, "preview"), p.token(), nil, &out)
		apRequireServedInline(t, out, f, "image/png")

		// The owner's raw body carries both values the outsider cases assert absent.
		status, body := doJSONStatus(t, http.MethodGet, apCalPreviewPath(p, calID, evtA, f.id, "preview"), p.token(), nil)
		require.Equalf(t, http.StatusOK, status, "body=%s", body)
		require.Contains(t, string(body), "previewUrl")
		require.Contains(t, string(body), f.storageKey)
	})

	t.Run("sibling event", func(t *testing.T) {
		apRequireSameRefusal(t, p.token(),
			apCalPreviewPath(p, calID, evtB, f.id, "preview"), apCalPreviewPath(p, calID, evtB, f.id, "download"),
			http.StatusNotFound, "CALENDAR.ATTACHMENT.NOT_FOUND", "attachment of another event")
	})

	t.Run("event under another calendar", func(t *testing.T) {
		apRequireSameRefusal(t, p.token(),
			apCalPreviewPath(p, otherCal, evtA, f.id, "preview"), apCalPreviewPath(p, otherCal, evtA, f.id, "download"),
			http.StatusNotFound, "CALENDAR.EVENT.NOT_FOUND", "event reached through another calendar")
	})

	t.Run("unknown attachment id", func(t *testing.T) {
		missing := uuid.Must(uuid.NewV7()).String()
		apRequireSameRefusal(t, p.token(),
			apCalPreviewPath(p, calID, evtA, missing, "preview"), apCalPreviewPath(p, calID, evtA, missing, "download"),
			http.StatusNotFound, "CALENDAR.ATTACHMENT.NOT_FOUND", "unknown attachment id")
	})

	t.Run("attachment id that is not a uuid", func(t *testing.T) {
		apRequireSameRefusal(t, p.token(),
			apCalPreviewPath(p, calID, evtA, "not-a-uuid", "preview"), apCalPreviewPath(p, calID, evtA, "not-a-uuid", "download"),
			http.StatusNotFound, "CALENDAR.ATTACHMENT.NOT_FOUND", "malformed attachment id")
	})

	t.Run("other tenant through the owner's workspace", func(t *testing.T) {
		apRequireSameRefusal(t, outsider.AccessToken,
			apCalPreviewPath(p, calID, evtA, f.id, "preview"), apCalPreviewPath(p, calID, evtA, f.id, "download"),
			http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED", "non-member of the workspace")
	})

	t.Run("other tenant through their own workspace", func(t *testing.T) {
		status, body := doJSONStatus(t, http.MethodGet,
			apCalPreviewPath(otherWs, calID, evtA, f.id, "preview"), outsider.AccessToken, nil)
		require.NotEqualf(t, http.StatusOK, status, "body=%s", body)
		require.NotContains(t, string(body), "previewUrl")
		require.NotContains(t, string(body), f.storageKey)
	})

	t.Run("no token", func(t *testing.T) {
		status, _ := doJSONStatus(t, http.MethodGet, apCalPreviewPath(p, calID, evtA, f.id, "preview"), "", nil)
		require.Equal(t, http.StatusUnauthorized, status)
	})
}

// Preview and download are separate audit actions, each written once per
// call; a refused preview writes nothing.
func TestAttachmentPreviewCalendarAudit(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	p := asPersona(t)
	calID := createCalendarForAttachments(t, p)
	evtID := createCalendarEventForAttachments(t, p, calID, "preview audit")
	img := apUploadCalendar(t, p, calID, evtID, "a.png", "image/png", makePNG(t, 8, 8, color.RGBA{R: 55, G: 56, B: 57, A: 255}))
	pdf := apUploadCalendar(t, p, calID, evtID, "doc.pdf", "application/pdf", []byte("%PDF-1.4\n%%EOF\n"))

	const previewAction, downloadAction = "calendar.attachment.preview", "calendar.attachment.download"
	count := func(action, att string) int { return apSettledAuditCount(t, p.workspaceID(), action, att) }

	require.Zero(t, count(previewAction, img.id))
	require.Zero(t, count(downloadAction, img.id))

	var out apPreviewBody
	doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, img.id, "preview"), p.token(), nil, &out)
	require.Equal(t, 1, count(previewAction, img.id), "one preview call writes one preview row")
	require.Zero(t, count(downloadAction, img.id), "a preview is not recorded as a download")

	doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, img.id, "preview"), p.token(), nil, &out)
	require.Equal(t, 2, count(previewAction, img.id), "every call is recorded")

	var dl struct {
		DownloadURL string `json:"downloadUrl"`
	}
	doJSON(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, img.id, "download"), p.token(), nil, &dl)
	require.Equal(t, 1, count(downloadAction, img.id), "control: download still writes its own row")
	require.Equal(t, 2, count(previewAction, img.id), "download leaves the preview rows alone")

	status, body := doJSONStatus(t, http.MethodGet, apCalPreviewPath(p, calID, evtID, pdf.id, "preview"), p.token(), nil)
	requireDenied(t, status, body, http.StatusUnsupportedMediaType, apNotPreviewable, "pdf preview")
	require.Zero(t, count(previewAction, pdf.id), "a refused preview writes no row")

	var rowType string
	require.NoError(t, testDB.QueryRow(`SELECT resource_type FROM audit_logs
		WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		  AND action = ? LIMIT 1`, p.workspaceID(), previewAction).Scan(&rowType))
	require.Equal(t, "calendar.attachment", rowType)
}
