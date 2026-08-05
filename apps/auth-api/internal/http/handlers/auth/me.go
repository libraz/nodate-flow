package auth

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

// strconvUint formats a uint64 in base 10 without bringing in the full
// strconv import surface at every call site. Local alias keeps rowToMe
// readable while still letting `go vet` see the strconv reference.
func strconvUint(v uint64) string { return strconv.FormatUint(v, 10) }

// Me handles GET /me. It reads the actor user id injected by the auth
// middleware and returns the matching user profile.
//
// The IsInstanceAdmin lookup is treated as best-effort: a transient
// query failure is logged and the flag stays false rather than failing
// the whole /me response, because the client uses /me primarily for
// session bootstrap and an unavailable admin signal is preferable to a
// blocked sign-in. Admin-only endpoints re-check the flag via the acl
// middleware, so a stale "not admin" never escalates privileges.
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
		isAdmin, adminErr := deps.Queries.AdminIsInstanceAdmin(ctx, uid)
		if adminErr != nil {
			slog.WarnContext(ctx, "me: instance admin lookup failed; defaulting to non-admin",
				slog.Uint64("user_id", uint64(uid)),
				slog.String("err", adminErr.Error()))
		} else {
			me.IsInstanceAdmin = isAdmin
		}
		return &MeOutput{Body: me}, nil
	}
}

// rowToMe maps a FindUserProfileByIdRow into the public Me DTO.
//
// Avatar URL precedence (post storage_objects refactor):
//
//  1. avatar_storage_object_id IS NOT NULL — user uploaded their own
//     avatar; surface a proxy URL of the form /avatars/{userId}?v={tag}
//     so the browser routes through GET /avatars/{userId}. The cache
//     buster is derived from the FK id, which advances whenever the
//     blob is replaced.
//  2. avatar_url IS NOT NULL — externally hosted (OIDC) URL; passed
//     through verbatim so the browser fetches it directly from the
//     provider.
//  3. Both NULL — nil pointer, no avatar.
//
// The proxy URL rewriter lives in [avatarutil.URLForClient] so any
// other service that surfaces the same column produces identical
// proxy URLs (note: avatarutil currently keys its cache buster off
// the storage_key string, which is now content-addressed; the same
// helper still works because it just hashes a stable suffix).
func rowToMe(row generated.FindUserProfileByIdRow, publicBaseURL string) MeBody {
	var avatar *string
	switch {
	case row.AvatarStorageObjectID.Valid:
		// Synthesize a fake "storage key" the avatarutil helper can
		// derive a cache buster from. The id is monotonic, so a
		// fresh upload produces a fresh URL and the browser cache
		// invalidates without us having to touch the URL shape.
		avatarID := uint64(row.AvatarStorageObjectID.Int32) //#nosec G115 -- avatar_storage_object_id is an unsigned DB id exposed via sql.NullInt32.
		key := "user/" + row.PublicID.String() + "/" + strconvUint(avatarID)
		s := avatarutil.URLForClient(key, row.PublicID.String(), publicBaseURL)
		avatar = &s
	case row.AvatarUrl.Valid:
		s := avatarutil.URLForClient(row.AvatarUrl.String, row.PublicID.String(), publicBaseURL)
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
		WeekStart:            string(row.WeekStart),
		ThemePreference:      string(row.ThemePreference),
		CalendarShiftDefault: string(row.CalendarShiftDefault),
		AvatarURL:            avatar,
		NotifEmailDigest:     row.NotifEmailDigestEnabled,
		NotifEmailMention:    row.NotifEmailMentionEnabled,
		NotifEmailAssignment: row.NotifEmailAssignmentEnabled,
		NotifEmailDueSoon:    row.NotifEmailDueSoonEnabled,
		NotifWebPush:         row.NotifWebPushEnabled,
	}
}
