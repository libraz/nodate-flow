// Package avatarutil resolves the avatar_url column stored on the users
// table into a URL safe to hand to a browser. It is shared so every
// service that surfaces a user profile presents avatars identically and
// no caller has to re-implement the cache-buster derivation rules.
package avatarutil

import (
	"fmt"
	"path"
	"strings"
)

// URLForClient decides whether a stored avatar value should be passed
// through verbatim (external OIDC provider URL) or rewritten into a
// proxy URL of the form "{publicBaseURL}/avatars/{userPublicID}?v={tag}".
//
// stored is the value of the users.avatar_url column. Three shapes are
// recognised:
//
//   - empty string — returns "" (callers should treat as "no avatar")
//   - URL beginning with "http://" or "https://" (case-insensitive) —
//     returned verbatim so the browser fetches it directly
//   - any other value — treated as a storage key like
//     "avatars/<userPublicID>/<attachmentPublicID>.jpg" and rewritten to
//     a proxy URL with a cache-busting "?v=" query.
//
// publicBaseURL is the externally-visible origin of the proxy service;
// trailing slashes are trimmed. When empty, the result is a relative
// URL ("/avatars/...") so callers behind a reverse proxy still produce
// a usable href.
func URLForClient(stored, userPublicID, publicBaseURL string) string {
	if stored == "" {
		return ""
	}
	lower := strings.ToLower(stored)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return stored
	}
	base := strings.TrimRight(publicBaseURL, "/")
	return fmt.Sprintf("%s/avatars/%s?v=%s", base, userPublicID, cacheBustFromKey(stored))
}

// cacheBustFromKey extracts a short, stable cache-busting token from a
// storage key like "avatars/<userPublicID>/<attachmentPublicID>.jpg".
// It returns the first 8 hex characters of the attachment filename
// (extension and dashes stripped). Because attachment public ids are
// UUID v7 the leading hex chunk advances every time a new avatar is
// uploaded, which forces a browser refresh without the caller having to
// compute any hash. Returns "0" when the key has no recognisable
// filename so the URL stays valid.
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
