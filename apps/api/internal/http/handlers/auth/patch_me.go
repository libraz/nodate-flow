package auth

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// PatchMe handles PATCH /me. Only fields explicitly supplied in the
// body are updated; nil fields leave the corresponding column untouched
// thanks to the COALESCE pattern in the underlying query.
func PatchMe(deps Deps) func(context.Context, *PatchMeInput) (*PatchMeOutput, error) {
	return func(ctx context.Context, in *PatchMeInput) (*PatchMeOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		params := generated.PatchMeParams{ID: uid}
		if in.Body.DisplayName != nil {
			params.DisplayName = sql.NullString{String: *in.Body.DisplayName, Valid: true}
		}
		if in.Body.Locale != nil {
			params.Locale = sql.NullString{String: *in.Body.Locale, Valid: true}
		}
		if in.Body.ThemePreference != nil {
			params.ThemePreference = generated.NullUsersThemePreference{
				UsersThemePreference: generated.UsersThemePreference(*in.Body.ThemePreference),
				Valid:                true,
			}
		}
		if in.Body.AvatarURL != nil {
			params.AvatarUrl = sql.NullString{String: *in.Body.AvatarURL, Valid: true}
		}
		if in.Body.NotifEmailDigest != nil {
			params.NotifEmailDigestEnabled = sql.NullBool{Bool: *in.Body.NotifEmailDigest, Valid: true}
		}
		if in.Body.NotifEmailMention != nil {
			params.NotifEmailMentionEnabled = sql.NullBool{Bool: *in.Body.NotifEmailMention, Valid: true}
		}
		if in.Body.NotifEmailAssignment != nil {
			params.NotifEmailAssignmentEnabled = sql.NullBool{Bool: *in.Body.NotifEmailAssignment, Valid: true}
		}
		if in.Body.NotifEmailDueSoon != nil {
			params.NotifEmailDueSoonEnabled = sql.NullBool{Bool: *in.Body.NotifEmailDueSoon, Valid: true}
		}
		if in.Body.NotifWebPush != nil {
			params.NotifWebPushEnabled = sql.NullBool{Bool: *in.Body.NotifWebPush, Valid: true}
		}

		if err := deps.Queries.PatchMe(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		row, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &PatchMeOutput{Body: rowToMe(row)}, nil
	}
}
