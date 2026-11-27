package asanaimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/attachkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

// errTooLarge stops a download past the upload ceiling.
var errTooLarge = errors.New("attachment exceeds the upload limit")

// limitWriter refuses bytes past left.
type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > l.left {
		return 0, errTooLarge
	}
	l.left -= int64(len(b))
	return l.w.Write(b)
}

// writeAttachments copies each missing attachment: download, reserve,
// upload, confirm (§4.6).
func (w *writer) writeAttachments(ctx context.Context) error {
	for _, ap := range w.p.Attachments {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &w.res.Projects[ap.Project].Attachments
		switch ap.Action {
		case ActionSkip:
			c.Skipped++
		case ActionUnchanged:
			c.Duplicates++
		case ActionCreate:
			ref, ok := w.tasks[ap.TaskGID]
			if !ok {
				c.Skipped++ // task not imported
				continue
			}
			w.copyAttachment(ctx, ap, ref, c)
		}
	}
	return nil
}

func (w *writer) copyAttachment(ctx context.Context, ap *AttachmentPlan, ref taskRef, c *Counts) {
	what := "attachment " + ap.GID
	if w.deps.Storage == nil || w.deps.Source == nil {
		w.fail(&c.Failed, what, errors.New("storage or the Asana download is not configured"))
		return
	}
	uploader, err := w.userID(ap.Uploader)
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	f, err := os.CreateTemp(w.deps.TempDir, "asana-attachment-*")
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	h := sha256.New()
	n, err := w.deps.Source.DownloadAttachment(ctx, ap.GID, &limitWriter{w: io.MultiWriter(f, h), left: storage.MaxUploadSize})
	if errors.Is(err, errTooLarge) {
		c.Skipped++ // too large
		return
	}
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	sum := h.Sum(nil)
	put := func(key, contentType string) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return w.deps.Storage.Put(ctx, key, f, n, contentType, hex.EncodeToString(sum))
	}
	size := uint64(n) //#nosec G115 -- a byte count, non-negative

	if ap.Row != nil {
		// A reserved row whose object never confirmed: fill the object it names.
		obj, err := w.q.FindStorageObjectByID(ctx, ap.Row.StorageObjectID)
		if err == nil && !bytes.Equal(obj.Sha256, sum) {
			err = errors.New("the file in Asana no longer matches the reserved upload")
		}
		if err == nil {
			err = put(obj.StorageKey, obj.ContentType)
		}
		if err == nil {
			err = attachkit.MarkUploaded(ctx, w.deps.DB, attachkit.MarkArgs{
				WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, StorageObjectID: obj.ID, Size: size,
			})
		}
		if err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
		c.Imported++
		return
	}

	res, err := attachkit.Reserve(ctx, w.deps.DB, attachkit.Args{
		WorkspaceID: w.p.Workspace.ID, WorkspacePublicID: w.p.Workspace.PublicID,
		TaskID: ref.id, TaskPublicID: ref.pub,
		Filename: ap.Name, ContentType: ap.ContentType, Size: size, SHA256: sum,
		SourceUID: SourceUID(ap.GID), UploaderUserID: uploader, Actor: w.actor, Store: w.deps.Storage,
	})
	if err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	taskID := int64(ref.id)
	w.rec.RecordTxAudit(ctx, w.actor, mutationlog.Mutation{
		AuditAction: "attachment.create", ResourceType: "attachment",
		ResourceID: res.AttachmentPublicID.String(), TaskID: &taskID,
		Payload: map[string]any{
			"taskId": ref.pub.String(), "attachmentId": res.AttachmentPublicID.String(), "filename": ap.Name,
		},
		CallSite: "asanaimport.Write (attachkit.Reserve appended the event)",
	})
	// The row stays unconfirmed on a failure here; the next run refills it.
	if !res.AlreadyUploaded {
		if err := put(res.StorageKey, ap.ContentType); err != nil {
			w.fail(&c.Failed, what, err)
			return
		}
	}
	if err := attachkit.MarkUploaded(ctx, w.deps.DB, attachkit.MarkArgs{
		WorkspaceID: w.p.Workspace.ID, TaskID: ref.id, StorageObjectID: res.StorageObjectID, Size: size,
	}); err != nil {
		w.fail(&c.Failed, what, err)
		return
	}
	c.Imported++
}
