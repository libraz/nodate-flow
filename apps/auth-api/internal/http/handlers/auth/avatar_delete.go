package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
)

// AvatarDeleteOutput returns the updated /me profile so the client can
// refresh its local user state without an extra round-trip.
type AvatarDeleteOutput struct {
	Body MeBody
}

// AvatarDelete handles DELETE /me/avatar.
//
// New flow (post storage_objects refactor):
//
//  1. Snapshot the current avatar_storage_object_id.
//  2. In one transaction: clear the FK on users, decrement
//     ref_count on the previously linked storage_objects row, and
//     attempt the unreferenced GC delete.
//  3. After commit, RemoveObject from MinIO if the GC delete fired.
//
// External avatar_url values (https://...) are also nulled out so the
// account ends up with no avatar at all rather than reverting to a
// stale OIDC URL the user previously cleared. The endpoint is
// idempotent: calling it again with no avatar present returns 200 with
// AvatarURL == nil.
func AvatarDelete(deps Deps) func(context.Context, *struct{}) (*AvatarDeleteOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*AvatarDeleteOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		prev, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		// Fast path: nothing to clean up. Skip the txn entirely so
		// the no-op call stays cheap.
		if !prev.AvatarStorageObjectID.Valid && !prev.AvatarUrl.Valid {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "user.avatar.delete",
				ActorID:      uid,
				ResourceType: "user",
			})
			row, err := deps.Queries.FindUserProfileById(ctx, uid)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			me := rowToMe(row, deps.PublicBaseURL)
			if isAdmin, aerr := deps.Queries.AdminIsInstanceAdmin(ctx, uid); aerr == nil {
				me.IsInstanceAdmin = isAdmin
			}
			return &AvatarDeleteOutput{Body: me}, nil
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)

		// Always clear the external avatar_url column too: the user
		// asked for "no avatar" and we should not silently revert to
		// a previously linked OIDC URL.
		if err := qtx.ClearMyAvatarURL(ctx, uid); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		var storageKeyToRemove string
		if prev.AvatarStorageObjectID.Valid {
			soID := uint32(prev.AvatarStorageObjectID.Int32) //#nosec G115 -- column is INT UNSIGNED, fits uint32

			if err := qtx.ClearMyAvatarStorageObject(ctx, uid); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			decRes, err := qtx.DecrementStorageObjectRefCount(ctx, soID)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if affected, _ := decRes.RowsAffected(); affected != 1 {
				slog.WarnContext(ctx, "avatar delete: storage_object ref_count underflow",
					slog.Uint64("storage_object_id", uint64(soID)),
				)
			}

			// Pre-read the storage key BEFORE the GC delete so we
			// still have it after a successful drop.
			fullRow, lookupErr := qtx.FindStorageObjectByID(ctx, soID)
			if lookupErr == nil {
				gcRes, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, soID)
				if err != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				if affected, _ := gcRes.RowsAffected(); affected == 1 {
					storageKeyToRemove = fullRow.StorageKey
				}
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if storageKeyToRemove != "" && deps.Storage != nil {
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := deps.Storage.RemoveObject(cleanupCtx, storageKeyToRemove); rmErr != nil {
				slog.WarnContext(cleanupCtx, "avatar delete: object delete failed",
					"err", rmErr, "key", storageKeyToRemove)
			}
			cleanupCancel()
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "user.avatar.delete",
			ActorID:      uid,
			ResourceType: "user",
		})

		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		me := rowToMe(row, deps.PublicBaseURL)
		if isAdmin, aerr := deps.Queries.AdminIsInstanceAdmin(ctx, uid); aerr == nil {
			me.IsInstanceAdmin = isAdmin
		}
		return &AvatarDeleteOutput{Body: me}, nil
	}
}
