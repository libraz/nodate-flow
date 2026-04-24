package auth

import (
	"context"
	"io"
	"log/slog"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
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
// NOT_FOUND is returned for:
//   - an unknown user public id,
//   - a user whose avatar_url is NULL,
//   - a user whose avatar_url is an external URL (the client should fetch
//     it directly, we do not proxy remote origins).
func AvatarProxy(deps Deps) func(context.Context, *AvatarProxyInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, in *AvatarProxyInput) (*huma.StreamResponse, error) {
		userID, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		row, err := deps.Queries.FindUserByPublicId(ctx, userID)
		if err != nil {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		if !row.AvatarUrl.Valid {
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		key := row.AvatarUrl.String
		if isExternalAvatarURL(key) {
			// External OIDC-provider URLs are not hosted by us and are
			// returned as-is by /me; the client should fetch them
			// directly rather than routing through this proxy.
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}
		if deps.Storage == nil {
			return nil, httpErr(apierrors.AuthAvatarStorageUnavailable)
		}

		body, info, err := deps.Storage.GetObject(ctx, key)
		if err != nil {
			slog.WarnContext(ctx, "avatar proxy: get object failed", "err", err, "key", key)
			return nil, httpErr(apierrors.AuthAvatarNotFound)
		}

		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				defer func() { _ = body.Close() }()

				contentType := info.ContentType
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
					slog.WarnContext(hctx.Context(), "avatar proxy: copy body failed", "err", werr, "key", key)
				}
			},
		}, nil
	}
}
