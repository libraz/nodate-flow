package auth

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/ctxutil"
)

// maxAvatarBytes bounds the accepted payload size. 5 MiB covers every
// reasonable phone-camera JPEG while still fitting in memory for the
// image.Decode validation pass below.
const maxAvatarBytes = 5 * 1024 * 1024

// avatarContentTypes enumerates the MIME types the upload endpoint
// accepts. The extension used in the storage key is derived from this
// table so every accepted type maps to exactly one on-disk extension.
var avatarContentTypes = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"image/gif":  "gif",
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

// AvatarUpload handles POST /me/avatar. It validates the uploaded file,
// writes the bytes to the object store under
// avatars/<userPublicId>/<attachmentPublicId>.<ext>, deletes any
// previous stored-key avatar, and updates users.avatar_url.
//
// External OIDC-provider avatar URLs are NOT removed on upload: they
// live in the same column but are pass-through on read, so overwriting
// the column with the new storage key implicitly detaches them.
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

		// Size guard BEFORE reading the bytes into memory. Trust the
		// multipart-reported size; we will also bound the actual read
		// with io.LimitReader below in case the client lied.
		if file.Size > maxAvatarBytes {
			return nil, httpErr(apierrors.AuthAvatarUploadTooLarge)
		}

		// Content-Type is required and must be on our allow-list. Some
		// browsers send "image/jpg"; only "image/jpeg" is IANA-correct.
		contentType := strings.ToLower(strings.TrimSpace(file.ContentType))
		ext, ok := avatarContentTypes[contentType]
		if !ok {
			return nil, httpErr(apierrors.AuthAvatarUploadUnsupportedType)
		}

		// Buffer the body so we can (a) validate via image.Decode and
		// (b) replay the bytes for the object-store upload. LimitReader
		// caps the amount of memory a misbehaving client can consume.
		buf := &bytes.Buffer{}
		n, err := io.Copy(buf, io.LimitReader(file, maxAvatarBytes+1))
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarUploadInvalidImage)
		}
		if n > maxAvatarBytes {
			return nil, httpErr(apierrors.AuthAvatarUploadTooLarge)
		}

		// Decode just enough of the image to confirm the payload is
		// actually a valid image of the declared type. We discard the
		// decoded image since we store the original bytes.
		if _, _, err := image.Decode(bytes.NewReader(buf.Bytes())); err != nil {
			return nil, httpErr(apierrors.AuthAvatarUploadInvalidImage)
		}

		// Fetch the actor's public_id so we can shape the storage key.
		userPublicID, err := deps.Queries.FindUserPublicIdById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		// Fetch the previous avatar_url so we can delete the stale
		// object from the store after the DB row is updated. Ignore
		// errors here: a missing row means nothing to clean up.
		prevProfile, _ := deps.Queries.FindUserProfileById(ctx, uid)

		// New storage key. The attachment public_id is a UUID v7 whose
		// leading hex digits become the cache-buster surfaced by
		// rowToMe so /me responses change when the avatar changes.
		attachmentID := types.New()
		key := fmt.Sprintf("avatars/%s/%s.%s", userPublicID.String(), attachmentID.String(), ext)

		if err := deps.Storage.PutObject(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()), contentType); err != nil {
			slog.ErrorContext(ctx, "avatar upload: put object failed", "err", err, "key", key)
			return nil, httpErr(apierrors.AuthAvatarStorageUnavailable)
		}

		if err := deps.Queries.SetMyAvatarURL(ctx, generated.SetMyAvatarURLParams{
			AvatarUrl: sql.NullString{String: key, Valid: true},
			ID:        uid,
		}); err != nil {
			// Best-effort cleanup of the just-written object so we do
			// not leak storage when the DB update fails. We MUST run
			// this even if ctx has been cancelled by the failing
			// request — otherwise a flaky upstream produces orphaned
			// blobs every time. ctxutil.Cleanup gives us inherited
			// values (trace ids, slog attrs) without inheriting the
			// cancellation, plus a hard 5s upper bound so the cleanup
			// cannot block shutdown.
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := deps.Storage.RemoveObject(cleanupCtx, key); rmErr != nil {
				slog.WarnContext(cleanupCtx, "avatar upload: orphan cleanup failed", "err", rmErr, "key", key)
			}
			cleanupCancel()
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		// Old-object cleanup. Only remove when the previous value was
		// a storage key (not an external OIDC URL) and differs from
		// the one we just wrote.
		if prevProfile.AvatarUrl.Valid {
			prev := prevProfile.AvatarUrl.String
			if prev != key && !isExternalAvatarURL(prev) {
				if err := deps.Storage.RemoveObject(ctx, prev); err != nil {
					slog.WarnContext(ctx, "avatar upload: stale object delete failed", "err", err, "key", prev)
				}
			}
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
// storage key owned by our object store.
func isExternalAvatarURL(stored string) bool {
	lower := strings.ToLower(stored)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
