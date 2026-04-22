package auth

import (
	"context"

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
		me := rowToMe(row)
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
func rowToMe(row generated.FindUserProfileByIdRow) MeBody {
	var avatar *string
	if row.AvatarUrl.Valid {
		s := row.AvatarUrl.String
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
