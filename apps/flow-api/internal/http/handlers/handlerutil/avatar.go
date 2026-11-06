package handlerutil

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

// AvatarURL projects a users.avatar_url value into a browser-ready URL:
// absolute http(s) URLs pass through, a storage key becomes the relative
// auth-api proxy URL. It returns "" when there is no avatar or the owning
// user's public id is unknown (a LEFT JOIN that found no user).
func AvatarURL(stored sql.NullString, userPublicID types.PublicID) string {
	if !stored.Valid || userPublicID == (types.PublicID{}) {
		return ""
	}
	return avatarutil.URLForClient(stored.String, userPublicID.String(), "")
}

// AvatarURLPtr is AvatarURL for response fields that omit an absent avatar.
func AvatarURLPtr(stored sql.NullString, userPublicID types.PublicID) *string {
	u := AvatarURL(stored, userPublicID)
	if u == "" {
		return nil
	}
	return &u
}
