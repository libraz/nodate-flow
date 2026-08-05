package admin

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// ListUserSessions handles GET /admin/users/{userId}/sessions. Resolves the
// user's internal id from the public id, then returns a paginated list of all
// sessions (including revoked ones) for that user.
func ListUserSessions(deps Deps) func(context.Context, *ListUserSessionsInput) (*ListUserSessionsOutput, error) {
	return func(ctx context.Context, in *ListUserSessionsInput) (*ListUserSessionsOutput, error) {
		pid, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceUserNotFound)
		}

		internalID, err := deps.Queries.AdminFindUserIdByPublicId(ctx, pid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceUserNotFound, apierrors.InternalUnexpected))
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.AdminListUserSessions(ctx, generated.AdminListUserSessionsParams{
			UserID: internalID,
			Limit:  limit,
			Offset: in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListUserSessionsOutput{}
		out.Body.Items = make([]Session, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToAdminSession(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// RevokeSession handles DELETE /admin/sessions/{sessionId}. Revokes a single
// session by its public id regardless of which user owns it.
func RevokeSession(deps Deps) func(context.Context, *RevokeSessionInput) (*RevokeSessionOutput, error) {
	return func(ctx context.Context, in *RevokeSessionInput) (*RevokeSessionOutput, error) {
		uid, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.SessionID)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		err = deps.Queries.AdminRevokeSession(ctx, pid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.session.revoke",
			ActorID:      uid,
			ResourceType: "session",
			ResourceID:   in.SessionID,
		})

		out := &RevokeSessionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
