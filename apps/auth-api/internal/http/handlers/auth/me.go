package auth

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/avatarutil"
)

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

// rowToMe maps a FindUserProfileByIdRow into the public Me DTO. It is
// the single place that converts the internal sql.NullString avatar
// column into a *string for the API boundary; the avatar URL rewriting
// itself lives in [avatarutil.URLForClient] so other services that
// surface the same column produce identical proxy URLs.
func rowToMe(row generated.FindUserProfileByIdRow, publicBaseURL string) MeBody {
	var avatar *string
	if row.AvatarUrl.Valid {
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
