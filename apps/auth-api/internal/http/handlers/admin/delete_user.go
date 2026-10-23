package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/teardown"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
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

		// Deleting is held to the rules suspending and removing a member
		// are: the instance keeps at least one enabled admin, and every
		// workspace the user owns keeps another owner. The guard locks both
		// owner sets inside the transaction that deletes the user.
		var answered error
		res, err := teardown.User(ctx, deps.DB, deps.Queries, deps.Storage, uid, pid.UUID(),
			func(ctx context.Context, tx *dbretry.Tx) error {
				answered = nil
				spec, err := ensureAdminRemains(ctx, tx, uid)
				if err == nil && spec == nil {
					spec, err = ensureWorkspacesKeepOwner(ctx, tx, uid)
				}
				if err != nil {
					return err
				}
				if spec != nil {
					answered = httpErr(spec)
					return errAnswered
				}
				return nil
			})
		if answered != nil {
			return nil, answered
		}
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

// ensureWorkspacesKeepOwner answers WS.MEMBER.LAST_OWNER when leaving is
// the only enabled owner of any workspace. It locks each owner set it
// reads, so it must run in the transaction that then deletes leaving.
func ensureWorkspacesKeepOwner(ctx context.Context, tx *dbretry.Tx, leaving uint32) (*apierr.Spec, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT workspace_id FROM workspace_members
		 WHERE user_id = ? AND role = 'owner' AND enabled = TRUE`, leaving)
	if err != nil {
		return nil, err
	}
	var owned []uint32
	for rows.Next() {
		var wsID uint32
		if err := rows.Scan(&wsID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		owned = append(owned, wsID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, wsID := range owned {
		err := memberkit.EnsureOwnerRemains(ctx, tx, memberkit.OwnerSet{
			Query: `SELECT user_id FROM workspace_members
			        WHERE workspace_id = ? AND role = 'owner' AND enabled = TRUE`,
			Args: []any{wsID},
		}, leaving)
		if errors.Is(err, memberkit.ErrLastOwner) {
			return apierrors.WsMemberLastOwner, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}
