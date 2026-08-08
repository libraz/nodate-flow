package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/teardown"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// DeleteUser handles DELETE /admin/users/{userId}, the admin
// force-delete for a user account.
//
// Pipeline (delegated to [teardown.User]): reconcile per-attachment
// ref_counts inside a transaction, hard-delete the user (CASCADE clears
// the attachment + avatar storage_objects rows), then best-effort sweep
// every newly orphaned MinIO blob.
//
// Self-delete is rejected with USER.DELETE.SELF_NOT_ALLOWED to keep an
// admin from accidentally locking themselves out of the panel; recovery
// would require another admin to undo it. Suspension (PATCH with
// enabled=false) is a separate, reversible operation and is NOT a
// precondition for this endpoint.
//
// Idempotent: if a concurrent delete won the race the response is 200
// with deleted=false rather than 404, and the in-tx ref_count decrements
// are rolled back so counters stay consistent.
func DeleteUser(deps Deps) func(context.Context, *DeleteUserInput) (*DeleteUserOutput, error) {
	return func(ctx context.Context, in *DeleteUserInput) (*DeleteUserOutput, error) {
		if in.Body.Confirm == nil || !*in.Body.Confirm {
			return nil, httpErr(apierrors.UserDeleteConfirmRequired)
		}

		actorID, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceUserNotFound)
		}

		uid, err := deps.Queries.AdminFindUserIdByPublicId(ctx, pid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Already gone — idempotent success.
				out := &DeleteUserOutput{}
				return out, nil
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Reject self-delete BEFORE running the teardown pipeline so the
		// admin gets a clear, dedicated error instead of a generic 400.
		if uid == actorID {
			return nil, httpErr(apierrors.UserDeleteSelfNotAllowed)
		}

		res, err := teardown.User(ctx, deps.DB, deps.Queries, deps.Storage, uid, pid.UUID())
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if res.Deleted {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "admin.user.delete",
				ActorID:      actorID,
				ResourceType: "user",
				ResourceID:   in.UserID,
				Metadata: map[string]any{
					"storage_objects_deleted": res.StorageObjectsDeleted,
					"minio_errors":            res.MinioErrors,
				},
			})
		}

		out := &DeleteUserOutput{}
		out.Body.Deleted = res.Deleted
		out.Body.StorageObjectsDeleted = res.StorageObjectsDeleted
		out.Body.MinioErrors = res.MinioErrors
		return out, nil
	}
}
