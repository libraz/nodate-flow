package auth

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Me handles GET /me. It reads the actor user id injected by the auth
// middleware and returns the matching user profile.
func Me(deps Deps) func(context.Context, *struct{}) (*MeOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		uid, ok := authn.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		me := rowToMe(row, deps.PublicBaseURL)
		isAdmin, err := deps.Queries.AdminIsInstanceAdmin(ctx, uid)
		if err == nil {
			me.IsInstanceAdmin = isAdmin
		}
		return &MeOutput{Body: me}, nil
	}
}

// rowToMe maps a FindUserProfileByIdRow into the public Me DTO. It is
// the single place that converts the internal sql.NullString avatar
// column into a *string for the API boundary.
//
// The avatar_url column holds either:
//
//   - NULL (no avatar),
//   - an external URL beginning with http:// or https:// (OIDC provider
//     avatar, passed through verbatim), or
//   - a storage key like "avatars/<userPublicId>/<attachmentPublicId>.jpg"
//     (uploaded via POST /me/avatar), which is rewritten into a proxy
//     URL served by GET /avatars/{userPublicId}.
//
// publicBaseURL is the externally-visible origin of the auth-api and
// must NOT end with a slash. When empty, storage-key avatars fall back
// to a relative URL so callers behind a reverse proxy still work.
func rowToMe(row generated.FindUserProfileByIdRow, publicBaseURL string) MeBody {
	var avatar *string
	if row.AvatarUrl.Valid {
		s := avatarURLForClient(row.AvatarUrl.String, row.PublicID.String(), publicBaseURL)
		avatar = &s
	}
	country := ""
	if row.Country.Valid {
		country = row.Country.String
	}
	return MeBody{
		ID:                   row.PublicID.String(),
		Email:                row.Email,
		DisplayName:          row.DisplayName,
		Locale:               row.Locale,
		Timezone:             row.Timezone,
		Country:              country,
		ThemePreference:      string(row.ThemePreference),
		AvatarURL:            avatar,
		NotifEmailDigest:     row.NotifEmailDigestEnabled,
		NotifEmailMention:    row.NotifEmailMentionEnabled,
		NotifEmailAssignment: row.NotifEmailAssignmentEnabled,
		NotifEmailDueSoon:    row.NotifEmailDueSoonEnabled,
		NotifWebPush:         row.NotifWebPushEnabled,
	}
}

// avatarURLForClient decides whether the stored avatar column should be
// passed through as an external URL or rewritten into a proxy URL that
// streams bytes from the object store via GET /avatars/{userPublicId}.
//
// External URLs (OIDC-provider avatars) are returned verbatim so the
// browser fetches them directly; they never round-trip through auth-api.
//
// Storage keys are rewritten to include a cache-buster derived from the
// attachment public_id so the browser re-fetches whenever the user
// uploads a new avatar, without the client having to compute any hash.
func avatarURLForClient(stored, userPublicID, publicBaseURL string) string {
	lower := strings.ToLower(stored)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return stored
	}
	base := strings.TrimRight(publicBaseURL, "/")
	return fmt.Sprintf("%s/avatars/%s?v=%s", base, userPublicID, cacheBustFromKey(stored))
}

// cacheBustFromKey extracts a short, stable cache-busting token from a
// storage key like "avatars/<userPublicId>/<attachmentPublicId>.jpg".
// It returns the first 8 characters of the attachment filename (minus
// extension), which is the leading hex of a UUID v7 and therefore
// changes every time a new avatar is uploaded. Returns "0" when the
// key does not match the expected shape so the URL remains valid.
func cacheBustFromKey(key string) string {
	if key == "" {
		return "0"
	}
	name := path.Base(key)
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	name = strings.ReplaceAll(name, "-", "")
	if len(name) >= 8 {
		return name[:8]
	}
	if name == "" {
		return "0"
	}
	return name
}
