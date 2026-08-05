// Package teardown holds the shared destructive-delete pipelines used by
// the workspace owner self-delete handler (DELETE /workspaces/{wsId}) and
// the admin force-delete handlers (DELETE /admin/workspaces/{wsId},
// DELETE /admin/users/{userId}).
//
// Both pipelines were lifted out of the deprecated
// `apps/auth-api/internal/http/handlers/admin/purge.go` so the three
// callers can share a single implementation. Callers remain responsible
// for the request-shaped concerns: parsing the path id, enforcing the
// `confirm: true` body check, mapping not-found into a 404, and emitting
// the audit entry.
package teardown

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
)

// errConcurrentDelete rolls a teardown transaction back when the target
// row turned out to be gone already: the pipeline's preparatory writes
// (attachment deletes, ref_count decrements) must not land on their own.
// It is an internal signal, never returned to a caller, and is not
// transient so the retry loop leaves it alone.
var errConcurrentDelete = stderrors.New("teardown: row already deleted")

// BlobSweeper is the subset of [storage.Client] the teardown pipelines
// need. Both pipelines take the interface rather than the concrete
// client so tests can observe what a failed delete did — or did not —
// remove from object storage.
type BlobSweeper interface {
	// RemoveObjects deletes the given keys in bulk, best effort. It
	// returns the first per-key failure after attempting the rest.
	RemoveObjects(ctx context.Context, keys []string) error
}

// sweepBlobs removes keys from object storage and reports 1 when the
// sweep had at least one failure, 0 otherwise (see
// [WorkspaceResult.MinioErrors] for why a bare flag).
//
// Both pipelines route every deletion through here so the ordering
// rule holds in one place: the sweep runs only after the owning
// database transaction has committed. Deleting blobs first means a
// transaction that later fails — a deadlock, a lock wait timeout, a
// cancelled request — leaves every row alive while the bytes are gone
// for good, which reads as "attachment present, download 404s" in the
// UI and cannot be undone.
//
// Committing first inverts the failure into an orphaned blob, which is
// recoverable — but only if the keys are written down: the rows that
// named them are already deleted, so this log is their last record.
// Each key is therefore logged individually rather than as a count.
//
// store is optional: it is nil when NF_S3_ENDPOINT is unset. A nil
// *storage.Client wrapped in an interface is not itself nil, so the
// guard has to unwrap the concrete type.
func sweepBlobs(ctx context.Context, store BlobSweeper, keys []string, owner slog.Attr) int64 {
	switch s := store.(type) {
	case nil:
		return 0
	case *storage.Client:
		if s == nil {
			return 0
		}
	}
	if len(keys) == 0 {
		return 0
	}
	err := store.RemoveObjects(ctx, keys)
	if err == nil {
		return 0
	}
	slog.WarnContext(ctx, "teardown: object storage sweep had errors",
		owner,
		slog.Int("key_count", len(keys)),
		slog.String("err", err.Error()),
	)
	// RemoveObjects collapses per-key failures into the first error, so
	// we cannot tell which keys survived. List them all: over-reporting
	// costs an operator one HEAD request per key, under-reporting costs
	// an unreclaimable blob.
	for _, k := range keys {
		slog.WarnContext(ctx, "teardown: blob may be orphaned",
			owner,
			slog.String("storage_key", k),
		)
	}
	return 1
}

// WorkspaceResult captures the observable side effects of [Workspace] so
// callers can populate the response body (Deleted, StorageObjectsDeleted,
// MinioErrors) and the audit metadata in one place.
type WorkspaceResult struct {
	// Deleted reports whether the workspaces row was actually removed by
	// HardDeleteWorkspace. False means a concurrent caller won the race
	// (RowsAffected == 0); the caller should respond 200 with deleted=false
	// for idempotency rather than 404.
	Deleted bool
	// StorageObjectsDeleted is the count of storage_objects keys the MinIO
	// driver attempted to delete. Reported as-is on the response so admins
	// can correlate it with bucket dashboards.
	StorageObjectsDeleted int64
	// MinioErrors is 1 when at least one MinIO key failed to delete and 0
	// otherwise. RemoveObjects collapses per-key failures into a single
	// first error, so we cannot count them precisely without re-walking;
	// surfacing 1 is enough for the UI to show "some blobs may be orphaned".
	MinioErrors int64
}

// Workspace runs the destructive workspace-delete pipeline against the
// supplied internal workspace id.
//
//  1. In one transaction: enumerate every storage_objects row owned by
//     the workspace (the CASCADE-anchored DELETE is about to remove the
//     rows that name the blobs, so the keys have to be read first, and
//     reading them inside the transaction keeps the list consistent with
//     what the DELETE actually removes), clear the two attachment tables
//     that reference storage_objects with ON DELETE RESTRICT, then run
//     the hard DELETE on the workspaces row. RowsAffected==0 means a
//     concurrent caller already deleted it; the transaction rolls back so
//     the attachment deletes do not land on their own, and the result
//     reports Deleted=false — the caller should treat that as idempotent
//     success, not 404.
//  2. Only once that transaction has committed, best-effort bulk-delete
//     the collected keys from MinIO. Failures are logged per key and
//     counted but DO NOT resurrect the workspace row. See [sweepBlobs]
//     for why the sweep must not run first.
//
// Why the attachment tables are cleared explicitly rather than left to the
// cascade: attachments.workspace_id and calendar_event_attachments.
// workspace_id are ON DELETE CASCADE, but both tables also reference
// storage_objects with ON DELETE RESTRICT, and storage_objects.workspace_id
// is itself ON DELETE CASCADE. Whether the workspace delete succeeds then
// depends on InnoDB reaching the attachment tables before storage_objects
// while walking the cascade chain — which follows table creation order and
// is not a documented guarantee. Deleting the referrers up front makes the
// outcome independent of it.
//
// The function never panics on a nil storage client (Storage is optional
// when NF_S3_ENDPOINT is unset); the MinIO sweep is simply skipped and
// the response reports zero counts.
func Workspace(ctx context.Context, db *sql.DB, q *generated.Queries, store BlobSweeper, wsID uint32) (WorkspaceResult, error) {
	var (
		keys    []string
		deleted bool
	)
	// Retried as a whole on a deadlock: the DELETE walks a wide cascade
	// and readily loses a lock race, and every attempt re-reads the keys
	// from scratch so a retry cannot carry state from the attempt that
	// rolled back.
	err := dbretry.InTx(ctx, db, "teardown.Workspace", nil, func(ctx context.Context, tx *sql.Tx) error {
		keys = nil
		deleted = false
		qtx := q.WithTx(tx)

		objs, lerr := qtx.ListStorageObjectsByWorkspace(ctx, sql.NullInt32{Int32: int32(wsID), Valid: true}) //#nosec G115 -- workspace internal id is INT UNSIGNED, fits int32 within realistic deployments
		if lerr != nil {
			return lerr
		}
		keys = make([]string, 0, len(objs))
		for _, o := range objs {
			if o.StorageKey != "" {
				keys = append(keys, o.StorageKey)
			}
		}

		if derr := qtx.DeleteAttachmentsByWorkspace(ctx, wsID); derr != nil {
			return derr
		}
		if derr := qtx.DeleteCalendarEventAttachmentsByWorkspace(ctx, wsID); derr != nil {
			return derr
		}

		res, derr := qtx.HardDeleteWorkspace(ctx, wsID)
		if derr != nil {
			return derr
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			// Raced with a concurrent delete. Roll back so the attachment
			// deletes are reverted along with it.
			return errConcurrentDelete
		}
		deleted = true
		return nil
	})
	if err != nil && !stderrors.Is(err, errConcurrentDelete) {
		return WorkspaceResult{}, err
	}
	if !deleted {
		// Either the transaction rolled back or another caller won the
		// race. Every row still points at its blob, so object storage
		// must be left alone.
		return WorkspaceResult{}, nil
	}

	minioErrors := sweepBlobs(ctx, store, keys,
		slog.Uint64("workspace_internal_id", uint64(wsID)))

	return WorkspaceResult{
		Deleted:               true,
		StorageObjectsDeleted: int64(len(keys)),
		MinioErrors:           minioErrors,
	}, nil
}

// UserResult mirrors [WorkspaceResult] for the user-delete pipeline. The
// counts reflect the union of avatar storage_objects (always orphaned by
// the user CASCADE) and shared attachment storage_objects whose ref_count
// truly reached zero after the per-attachment decrement.
type UserResult struct {
	// Deleted reports whether the users row was actually removed by
	// HardDeleteUser. False means a concurrent caller won the race
	// (RowsAffected == 0); the per-attachment ref_count decrements are
	// rolled back so the database is left consistent.
	Deleted bool
	// StorageObjectsDeleted is the count of MinIO keys the driver
	// attempted to delete (avatars + freed shared attachments).
	StorageObjectsDeleted int64
	// MinioErrors is 1 when at least one MinIO key failed to delete and 0
	// otherwise. See [WorkspaceResult.MinioErrors] for rationale.
	MinioErrors int64
}

// User runs the destructive user-delete pipeline against the supplied
// internal user id.
//
// Why the ref_count dance rather than a straight enumerate-and-sweep:
// attachments.uploader_id is ON DELETE CASCADE, so HardDeleteUser
// silently removes the user's attachment rows. Sweeping from a list read
// up front would have to guess which underlying storage_objects rows are
// about to lose their last referrer, and a concurrent dedup-hit upload
// from a sibling member could bump ref_count between the guess and
// the cascade — yanking the blob out from under the new uploader. Instead
// we DecrementStorageObjectRefCount for every uploader-owned attachment
// row, run HardDeleteUser inside the same transaction (CASCADE drops the
// attachment rows so the FK RESTRICT on storage_objects no longer blocks
// GC), then DeleteStorageObjectIfUnreferenced atomically removes only
// those rows whose ref_count truly reached zero. The MinIO sweep that
// follows is best-effort.
//
// Phases:
//
//  1. Read-only enumeration (outside the tx): list every storage_objects
//     row referenced by the user — task attachments uploaded by them,
//     calendar event attachments uploaded by them, and avatar
//     storage_objects owned directly by them.
//  2. Atomic mutation (inside the tx): decrement per-attachment ref_counts,
//     hard-delete the user (CASCADE clears the attachment + avatar SO
//     rows), then atomically GC any storage_objects whose ref_count truly
//     reached zero. RowsAffected==0 from HardDeleteUser triggers a
//     rollback so the decrements do not corrupt counters.
//  3. Best-effort MinIO sweep (outside the tx): combine the avatar keys
//     (always orphaned by CASCADE) with the shared SO keys whose ref_count
//     truly reached zero. Failures are logged and counted but do not roll
//     back the DB delete.
//
// The function never panics on a nil storage client; the MinIO sweep is
// simply skipped and the response reports zero counts.
func User(ctx context.Context, db *sql.DB, q *generated.Queries, store BlobSweeper, userID uint32) (UserResult, error) {
	taskAtts, err := q.ListAttachmentsForUploaderPurge(ctx, userID)
	if err != nil {
		return UserResult{}, err
	}

	calAtts, err := q.ListCalendarEventAttachmentsForUploaderPurge(ctx, userID)
	if err != nil {
		return UserResult{}, err
	}

	avatarObjs, err := q.ListStorageObjectsByOwnerUser(ctx, sql.NullInt32{Int32: int32(userID), Valid: true}) //#nosec G115 -- user internal id is INT UNSIGNED, fits int32 within realistic deployments
	if err != nil {
		return UserResult{}, err
	}

	// Build a unique set of storage_object IDs from the attachment lists
	// (task + calendar) and remember one storage_key per ID for the MinIO
	// sweep. Storage keys are immutable per row, so any sample is fine.
	sharedKeyByID := make(map[uint32]string, len(taskAtts)+len(calAtts))
	for _, a := range taskAtts {
		if a.StorageKey != "" {
			sharedKeyByID[a.StorageObjectID] = a.StorageKey
		}
	}
	for _, a := range calAtts {
		if a.StorageKey != "" {
			sharedKeyByID[a.StorageObjectID] = a.StorageKey
		}
	}

	var (
		freedSharedKeys []string
		deleted         bool
	)
	// Retried as a whole on a deadlock, same as the workspace pipeline.
	// freedSharedKeys is rebuilt per attempt: an attempt that rolls back
	// must not leave keys behind for the sweep that follows.
	err = dbretry.InTx(ctx, db, "teardown.User", nil, func(ctx context.Context, tx *sql.Tx) error {
		freedSharedKeys = nil
		deleted = false
		qtx := q.WithTx(tx)

		for _, a := range taskAtts {
			decRes, derr := qtx.DecrementStorageObjectRefCount(ctx, a.StorageObjectID)
			if derr != nil {
				return derr
			}
			if affected, _ := decRes.RowsAffected(); affected != 1 {
				slog.WarnContext(ctx, "teardown: user delete ref_count underflow on task attachment",
					slog.Uint64("storage_object_id", uint64(a.StorageObjectID)),
					slog.Uint64("user_internal_id", uint64(userID)),
				)
			}
		}
		for _, a := range calAtts {
			decRes, derr := qtx.DecrementStorageObjectRefCount(ctx, a.StorageObjectID)
			if derr != nil {
				return derr
			}
			if affected, _ := decRes.RowsAffected(); affected != 1 {
				slog.WarnContext(ctx, "teardown: user delete ref_count underflow on calendar attachment",
					slog.Uint64("storage_object_id", uint64(a.StorageObjectID)),
					slog.Uint64("user_internal_id", uint64(userID)),
				)
			}
		}

		delRes, derr := qtx.HardDeleteUser(ctx, userID)
		if derr != nil {
			return derr
		}
		rows, _ := delRes.RowsAffected()
		if rows == 0 {
			// Raced with a concurrent delete. Roll back so the
			// ref_count decrements are reverted.
			return errConcurrentDelete
		}
		deleted = true

		// CASCADE has now removed every attachment row pointing at the
		// SOs above; DeleteStorageObjectIfUnreferenced will succeed
		// exactly for those whose ref_count is genuinely zero (sole-
		// referrer purge), and no-op for shared blobs where a sibling
		// user still references them.
		for soID, key := range sharedKeyByID {
			gcRes, gerr := qtx.DeleteStorageObjectIfUnreferenced(ctx, soID)
			if gerr != nil {
				return gerr
			}
			if affected, _ := gcRes.RowsAffected(); affected == 1 {
				freedSharedKeys = append(freedSharedKeys, key)
			}
		}

		return nil
	})
	if err != nil && !stderrors.Is(err, errConcurrentDelete) {
		return UserResult{}, err
	}

	// Combine avatar keys (always orphaned by CASCADE when we got
	// deleted=true) with the shared SO keys whose ref_count truly reached
	// zero. Deduplicate so an avatar that happens to share a storage_key
	// with an attachment is only swept once.
	keySet := make(map[string]struct{}, len(avatarObjs)+len(freedSharedKeys))
	if deleted {
		for _, o := range avatarObjs {
			if o.StorageKey != "" {
				keySet[o.StorageKey] = struct{}{}
			}
		}
		for _, k := range freedSharedKeys {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}

	var minioErrors int64
	if deleted {
		minioErrors = sweepBlobs(ctx, store, keys,
			slog.Uint64("user_internal_id", uint64(userID)))
	}

	return UserResult{
		Deleted:               deleted,
		StorageObjectsDeleted: int64(len(keys)),
		MinioErrors:           minioErrors,
	}, nil
}
