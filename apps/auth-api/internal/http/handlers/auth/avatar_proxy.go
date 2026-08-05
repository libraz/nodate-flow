package auth

import (
	"context"
	"io"
	"log/slog"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// AvatarProxyInput binds the path param for GET /avatars/{userId}.
// userId is a UUID v7 public id — the internal autoincrement column is
// never exposed to the caller.
type AvatarProxyInput struct {
	UserID string `path:"userId" doc:"User public id (UUID v7)"`
}

// AvatarProxy handles GET /avatars/{userId}. It streams the avatar
// bytes directly from the object store to the HTTP response so the
// browser can render <img src="…/avatars/xyz"> without a signed URL.
//
// The endpoint is public (no bearer token required) on purpose: avatar
// URLs are meant to be shared in page content and email. The response
// carries ETag + Cache-Control so browsers can revalidate cheaply.
//
// The proxy ONLY serves uploaded blobs (avatar_storage_object_id IS NOT
// NULL). Externally hosted avatar_url values (https://...) are returned
// verbatim by the /me endpoint and the client fetches them directly —
// proxying through us would hide the actual origin and force every
// browser cache to bounce off our CDN.
//
// NOT_FOUND is returned for:
//   - an unknown user public id,
//   - a user with no uploaded avatar (avatar_storage_object_id IS NULL).
func AvatarProxy(deps Deps) func(context.Context, *AvatarProxyInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, in *AvatarProxyInput) (*huma.StreamResponse, error) {
		userPublic, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}

		// Resolve the internal user id and the linked storage object
		// in two cheap lookups. We cannot reach into v_users for the
		// joined storage_objects.public_id from this package without
		// adding a query, so the two-step resolve keeps the change
		// surface minimal at the cost of one extra round trip.
		userID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, userPublic)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		profile, err := deps.Queries.FindUserProfileById(ctx, userID)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		if !profile.AvatarStorageObjectID.Valid {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		soID := uint32(profile.AvatarStorageObjectID.Int32) //#nosec G115 -- column is INT UNSIGNED, fits uint32
		so, err := deps.Queries.FindStorageObjectByID(ctx, soID)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		if deps.Storage == nil {
			return nil, httpErr(apierrors.AuthAvatarStorageUnavailable)
		}

		body, info, err := deps.Storage.GetObject(ctx, so.StorageKey)
		if err != nil {
			slog.WarnContext(ctx, "avatar proxy: get object failed", "err", err, "key", so.StorageKey)
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}

		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				defer func() { _ = body.Close() }()

				contentType := info.ContentType
				if contentType == "" {
					contentType = so.ContentType
				}
				if contentType == "" {
					contentType = "image/jpeg"
				}
				hctx.SetHeader("Content-Type", contentType)
				if info.Size > 0 {
					hctx.SetHeader("Content-Length", strconv.FormatInt(info.Size, 10))
				}
				if info.ETag != "" {
					hctx.SetHeader("ETag", info.ETag)
				}
				hctx.SetHeader("Cache-Control", "public, max-age=600, stale-while-revalidate=86400")
				hctx.SetStatus(200)

				if _, werr := io.Copy(hctx.BodyWriter(), body); werr != nil {
					slog.WarnContext(hctx.Context(), "avatar proxy: copy body failed", "err", werr, "key", so.StorageKey)
				}
			},
		}, nil
	}
}
