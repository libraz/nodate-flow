package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"image"

	// Blank imports register JPEG/PNG/GIF decoders with image.Decode so
	// we can verify that an uploaded payload is really an image before
	// writing it to the object store.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	// Registers the WebP decoder with image.Decode. The package has no
	// runtime dependencies other than standard library.
	_ "golang.org/x/image/webp"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
)

// maxAvatarBytes bounds the accepted payload size. 5 MiB covers every
// reasonable phone-camera JPEG while still fitting in memory for the
// image.Decode validation pass below.
const maxAvatarBytes = 5 * 1024 * 1024

// avatarUploadMaxAttempts bounds the duplicate-entry retry loop. If
// FindStorageObjectByOwnerUserSha returns ErrNoRows but
// InsertStorageObject hits MySQL 1062 (a concurrent upload from the
// same user beat us), we must restart the transaction so the next
// attempt's REPEATABLE READ snapshot can observe the winning row.
// Same-tx re-find still misses because the snapshot is pinned at the
// first SELECT. Three attempts is a defensive ceiling; in practice
// convergence happens on attempt 1.
const avatarUploadMaxAttempts = 3

// avatarContentTypes enumerates the MIME types the upload endpoint
// accepts. Surfaced for input validation only — the storage key is now
// content-addressed by sha256, not by extension, so the value is no
// longer woven into a path.
var avatarContentTypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
	"image/gif":  {},
}

// AvatarUploadInput binds the multipart/form-data body for POST /me/avatar.
// The single "file" field carries the raw image bytes; Huma parses the
// multipart envelope and hands us a FormFile with the claimed Content-Type.
type AvatarUploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" contentType:"image/jpeg,image/png,image/webp,image/gif" required:"true"`
	}]
}

// AvatarUploadOutput mirrors the shape of /me so the client can update
// its local user state without an extra round-trip.
type AvatarUploadOutput struct {
	Body MeBody
}

// AvatarUpload handles POST /me/avatar.
//
// New flow (post storage_objects refactor):
//
//  1. Read the body, validate size, decode as image to confirm MIME.
//  2. Compute the SHA-256 of the bytes.
//  3. In one transaction: look up an existing user-scoped
//     storage_objects row by (owner_user_id, sha256). On a hit bump
//     ref_count; on a miss PUT the bytes to MinIO under the
//     content-addressed key user/{userPublicHex}/{sha256Hex} then
//     INSERT a new storage_objects row.
//  4. Update users.avatar_storage_object_id to the (existing or new)
//     id and clear the externally-hosted users.avatar_url so the
//     uploaded image becomes the source of truth.
//  5. Decrement the ref_count on the previously linked storage_objects
//     row (if different) and GC the underlying blob if no references
//     remain. The MinIO delete runs after commit on a best-effort basis.
//
// External OIDC-provider avatar URLs (https://...) live on the
// avatar_url column and are pass-through. They are cleared whenever an
// uploaded avatar takes over so the proxy URL becomes canonical.
func AvatarUpload(deps Deps) func(context.Context, *AvatarUploadInput) (*AvatarUploadOutput, error) {
	return func(ctx context.Context, in *AvatarUploadInput) (*AvatarUploadOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		if deps.Storage == nil {
			return nil, httpErr(apierrors.AuthAvatarStorageUnavailable)
		}

		file := in.RawBody.Data().File
		if !file.IsSet {
			return nil, httpErr(apierrors.AuthAvatarUploadInvalidImage)
		}
		defer func() {
			_ = file.Close()
		}()

		if file.Size > maxAvatarBytes {
			return nil, httpErr(apierrors.AuthAvatarUploadTooLarge)
		}

		contentType := strings.ToLower(strings.TrimSpace(file.ContentType))
		if _, ok := avatarContentTypes[contentType]; !ok {
			return nil, httpErr(apierrors.AuthAvatarUploadUnsupportedType)
		}

		buf := &bytes.Buffer{}
		n, err := io.Copy(buf, io.LimitReader(file, maxAvatarBytes+1))
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarUploadInvalidImage)
		}
		if n > maxAvatarBytes {
			return nil, httpErr(apierrors.AuthAvatarUploadTooLarge)
		}

		if _, _, err := image.Decode(bytes.NewReader(buf.Bytes())); err != nil {
			return nil, httpErr(apierrors.AuthAvatarUploadInvalidImage)
		}

		sum := sha256.Sum256(buf.Bytes())
		shaBytes := sum[:]
		shaHex := hex.EncodeToString(shaBytes)

		userPublicID, err := deps.Queries.FindUserPublicIdById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		userHex := hex.EncodeToString(userPublicID[:])

		// Snapshot the current avatar_storage_object_id BEFORE the
		// txn so we know which previous row (if any) needs its
		// ref_count dropped at the end.
		prevProfile, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		// Concurrent-race retry loop. Under REPEATABLE READ a same-tx
		// re-find after MySQL 1062 still misses the winning racer's
		// committed row, so we roll the tx back and start fresh until
		// the SELECT either sees the dedup target or wins the INSERT
		// outright. didPutToStorage tracks whether we PUT bytes to
		// MinIO so commit failure on the miss path can clean them up;
		// across retries the key is content-addressed by sha256, so
		// re-PUTing on attempt N >= 1 is idempotent and the bytes
		// remain valid even if a later attempt dedups onto a winner
		// (the winner row references the same key, same bytes).
		var (
			storageObjectID        uint32
			didPutToStorage        bool
			finalAttemptDeduped    bool
			prevStorageKeyToRemove string
			committed              bool
		)

	attempts:
		for attempt := 0; attempt < avatarUploadMaxAttempts; attempt++ {
			tx, err := deps.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rolledBack := false
			rollback := func() {
				if !rolledBack {
					_ = tx.Rollback()
					rolledBack = true
				}
			}
			qtx := deps.Queries.WithTx(tx)

			// Reset per-attempt state. storageKey is restored from
			// the content-addressed default; a dedup hit further
			// down may overwrite it with the winner's recorded key.
			storageKey := storage.KeyForUser(userHex, shaHex)
			prevStorageKeyToRemove = ""
			finalAttemptDeduped = false

			existing, lookupErr := qtx.FindStorageObjectByOwnerUserSha(ctx, generated.FindStorageObjectByOwnerUserShaParams{
				OwnerUserID: sql.NullInt32{Int32: int32(uid), Valid: true}, //#nosec G115 -- user id sourced from session, fits int32 within realistic deployments
				Sha256:      shaBytes,
			})
			switch {
			case lookupErr == nil:
				// Dedup hit: bump ref_count on the existing row.
				if err := qtx.IncrementStorageObjectRefCount(ctx, existing.ID); err != nil {
					rollback()
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				storageObjectID = existing.ID
				storageKey = existing.StorageKey
				finalAttemptDeduped = true
			case stderrors.Is(lookupErr, sql.ErrNoRows):
				// Miss: write to MinIO first so we never insert a
				// storage_objects row pointing at a nonexistent blob.
				// Skip the PUT on retries when we already wrote the
				// bytes on a prior attempt — the key is sha-addressed
				// so the object is identical and still in place.
				if !didPutToStorage {
					if err := deps.Storage.PutObject(ctx, storageKey, bytes.NewReader(buf.Bytes()), int64(buf.Len()), contentType); err != nil {
						slog.ErrorContext(ctx, "avatar upload: put object failed", "err", err, "key", storageKey)
						rollback()
						return nil, httpErr(apierrors.AuthAvatarStorageUnavailable)
					}
					didPutToStorage = true
				}
				soPub := types.New()
				insRes, insErr := qtx.InsertStorageObject(ctx, generated.InsertStorageObjectParams{
					PublicID:    soPub,
					WorkspaceID: sql.NullInt32{},
					OwnerUserID: sql.NullInt32{Int32: int32(uid), Valid: true}, //#nosec G115 -- user id sourced from session, fits int32 within realistic deployments
					Sha256:      shaBytes,
					ByteSize:    uint64(buf.Len()), //#nosec G115 -- buf.Len bounded by maxAvatarBytes, fits uint64
					ContentType: contentType,
					StorageKey:  storageKey,
				})
				switch {
				case insErr == nil:
					lastID, idErr := insRes.LastInsertId()
					if idErr != nil || lastID <= 0 {
						rollback()
						return nil, httpErr(apierrors.InternalUnexpected)
					}
					storageObjectID = uint32(lastID) //#nosec G115 -- AUTO_INCREMENT id fits uint32 within realistic deployments
				case handlerutil.IsDuplicateEntry(insErr):
					// Race lost: a concurrent upload from the same
					// user inserted the (owner_user_id, sha256) row
					// first. Roll the tx back and retry; the next
					// attempt's REPEATABLE READ snapshot will
					// observe the winner and dedup onto it. The
					// PutObject above is harmless either way: the
					// storage key is content-addressed by sha256 so
					// both racers wrote identical bytes to the same
					// key, and the winner's row points at it. We
					// keep didPutToStorage=true so a later commit
					// failure can still clean up the orphan, but
					// since the winner references the key the
					// post-commit cleanup branch never fires on the
					// dedup-success path.
					rollback()
					continue attempts
				default:
					rollback()
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			default:
				rollback()
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			// Bind the new storage object to the user and clear any
			// stale external avatar_url so the proxy URL is the source
			// of truth.
			if err := qtx.SetMyAvatarStorageObject(ctx, generated.SetMyAvatarStorageObjectParams{
				AvatarStorageObjectID: sql.NullInt32{Int32: int32(storageObjectID), Valid: true}, //#nosec G115 -- AUTO_INCREMENT id fits int32 within realistic deployments
				ID:                    uid,
			}); err != nil {
				rollback()
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if err := qtx.SetMyAvatarURL(ctx, generated.SetMyAvatarURLParams{
				AvatarUrl: sql.NullString{},
				ID:        uid,
			}); err != nil {
				rollback()
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			// Drop the ref_count on the previously linked storage_objects
			// row (if any) and remember its key for post-commit GC.
			if prevProfile.AvatarStorageObjectID.Valid && uint32(prevProfile.AvatarStorageObjectID.Int32) != storageObjectID { //#nosec G115 -- column is INT UNSIGNED, fits uint32
				prevID := uint32(prevProfile.AvatarStorageObjectID.Int32) //#nosec G115 -- as above
				decRes, err := qtx.DecrementStorageObjectRefCount(ctx, prevID)
				if err != nil {
					rollback()
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				if affected, _ := decRes.RowsAffected(); affected != 1 {
					slog.WarnContext(ctx, "previous avatar storage_object ref_count underflow",
						slog.Uint64("storage_object_id", uint64(prevID)),
						slog.String("handler", "auth.AvatarUpload"),
					)
				}
				prevFull, lookupErr := qtx.FindStorageObjectByID(ctx, prevID)
				if lookupErr == nil {
					gcRes, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, prevID)
					if err != nil {
						rollback()
						return nil, httpErr(apierrors.InternalUnexpected)
					}
					if affected, _ := gcRes.RowsAffected(); affected == 1 {
						prevStorageKeyToRemove = prevFull.StorageKey
					}
				}
			}

			if err := tx.Commit(); err != nil {
				// Commit failed; the tx is gone. Clean up the
				// just-written object so we do not leak storage —
				// EXCEPT when this attempt ended up as a dedup hit,
				// because in that case the winner's storage_objects
				// row references the same content-addressed key and
				// deleting it would corrupt the dedup target.
				rolledBack = true
				if didPutToStorage && !finalAttemptDeduped {
					cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
					if rmErr := deps.Storage.RemoveObject(cleanupCtx, storageKey); rmErr != nil {
						slog.WarnContext(cleanupCtx, "avatar upload: orphan cleanup failed", "err", rmErr, "key", storageKey)
					}
					cleanupCancel()
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rolledBack = true
			committed = true
			break attempts
		}
		if !committed {
			// Retry ceiling reached. Practically unreachable; the
			// 1062 retry path converges in <=2 attempts even under
			// many-way contention. Do NOT delete the PUT bytes: by
			// definition we only got here by losing every retry to
			// MySQL 1062, which means a concurrent winner's
			// storage_objects row now references the
			// content-addressed key we wrote to. Removing it would
			// corrupt the winner. The next GC sweeper pass will
			// clean up if this assumption ever breaks.
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Best-effort GC of the previous blob after commit. Failures
		// are logged but do not affect the response: the next GC
		// sweeper pass will catch any orphans.
		if prevStorageKeyToRemove != "" {
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := deps.Storage.RemoveObject(cleanupCtx, prevStorageKeyToRemove); rmErr != nil {
				slog.WarnContext(cleanupCtx, "avatar upload: stale object delete failed", "err", rmErr, "key", prevStorageKeyToRemove)
			}
			cleanupCancel()
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "user.avatar.upload",
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
		return &AvatarUploadOutput{Body: me}, nil
	}
}

// isExternalAvatarURL reports whether the stored avatar_url value is a
// pass-through http(s) URL (e.g. a Google profile image) rather than a
// storage key owned by our object store. Retained because rowToMe and
// the OIDC callbacks still need to detect external URLs to decide
// whether to issue a proxy URL.
func isExternalAvatarURL(stored string) bool {
	lower := strings.ToLower(stored)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
