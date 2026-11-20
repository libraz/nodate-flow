package handlerutil

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

// AvatarURL projects a user's avatar columns into a browser-ready URL. An
// uploaded storage object wins and becomes the relative auth-api proxy URL
// tagged with an opaque cache key; otherwise users.avatar_url is used
// (absolute http(s) URLs pass through, a storage key becomes the proxy URL).
// It returns "" when there is no avatar or the owning user's public id is
// unknown (a LEFT JOIN that found no user).
func AvatarURL(stored sql.NullString, storageObjectID sql.NullInt32, userPublicID types.PublicID) string {
	if userPublicID == (types.PublicID{}) {
		return ""
	}
	return avatarutil.Resolve(storageObjectID, stored, userPublicID.String(), "")
}

// AvatarURLPtr is AvatarURL for response fields that omit an absent avatar.
func AvatarURLPtr(stored sql.NullString, storageObjectID sql.NullInt32, userPublicID types.PublicID) *string {
	if userPublicID == (types.PublicID{}) {
		return nil
	}
	return avatarutil.ResolvePtr(storageObjectID, stored, userPublicID.String(), "")
}
