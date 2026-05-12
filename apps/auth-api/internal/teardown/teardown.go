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
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/storage"
)

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
//  1. Enumerate every storage_objects row owned by the workspace so the
//     MinIO sweep can run before the CASCADE-anchored hard DELETE removes
//     the rows we would otherwise need to drive the blob cleanup.
//  2. Best-effort bulk-delete those keys from MinIO. Failures are logged
//     and counted but DO NOT abort the DB delete; the alternative is
//     leaving the workspace row alive forever whenever object storage
//     hiccups. Orphaned blobs can be reaped by a separate sweeper.
//  3. CASCADE-anchored hard DELETE on the workspaces row. RowsAffected==0
//     means a concurrent caller already deleted it; the result reports
//     Deleted=false and the caller should treat that as idempotent
//     success, not 404.
//
// The function never panics on a nil storage client (Storage is optional
// when NF_S3_ENDPOINT is unset); the MinIO sweep is simply skipped and
// the response reports zero counts.
func Workspace(ctx context.Context, db *sql.DB, q *generated.Queries, store *storage.Client, wsID uint32) (WorkspaceResult, error) {
	_ = db // reserved for future tx-scoped variants; kept for caller-side parity with [User].

	objs, err := q.ListStorageObjectsByWorkspace(ctx, sql.NullInt32{Int32: int32(wsID), Valid: true}) //#nosec G115 -- workspace internal id is INT UNSIGNED, fits int32 within realistic deployments
	if err != nil {
		return WorkspaceResult{}, err
	}

	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		if o.StorageKey != "" {
			keys = append(keys, o.StorageKey)
		}
	}

	var minioErrors int64
	if store != nil && len(keys) > 0 {
		if rerr := store.RemoveObjects(ctx, keys); rerr != nil {
			slog.WarnContext(ctx, "teardown: workspace delete MinIO sweep had errors",
				slog.Uint64("workspace_internal_id", uint64(wsID)),
				slog.Int("key_count", len(keys)),
				slog.String("err", rerr.Error()),
			)
			minioErrors = 1
		}
	}

	res, err := q.HardDeleteWorkspace(ctx, wsID)
	if err != nil {
		return WorkspaceResult{}, err
	}
	rows, _ := res.RowsAffected()

	return WorkspaceResult{
		Deleted:               rows > 0,
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
// Why a transaction-then-sweep flow (instead of the workspace pattern's
// "MinIO first, DB second"): attachments.uploader_id is ON DELETE CASCADE,
// so HardDeleteUser silently removes the user's attachment rows. A naive
// MinIO-first sweep would have to guess which underlying storage_objects
// rows are about to lose their last referrer, and a concurrent dedup-hit
// upload from a sibling member could bump ref_count between the guess and
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
func User(ctx context.Context, db *sql.DB, q *generated.Queries, store *storage.Client, userID uint32) (UserResult, error) {
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
	err = func() error {
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			return txErr
		}
		defer func() { _ = tx.Rollback() }()
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
			return nil
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

		return tx.Commit()
	}()
	if err != nil {
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
	if deleted && store != nil && len(keys) > 0 {
		if rerr := store.RemoveObjects(ctx, keys); rerr != nil {
			slog.WarnContext(ctx, "teardown: user delete MinIO sweep had errors",
				slog.Uint64("user_internal_id", uint64(userID)),
				slog.Int("key_count", len(keys)),
				slog.String("err", rerr.Error()),
			)
			minioErrors = 1
		}
	}

	return UserResult{
		Deleted:               deleted,
		StorageObjectsDeleted: int64(len(keys)),
		MinioErrors:           minioErrors,
	}, nil
}
