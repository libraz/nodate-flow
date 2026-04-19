package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ListAdmins handles GET /admin/instance-admins. Returns a paginated list of
// all active instance administrator grants.
func ListAdmins(deps Deps) func(context.Context, *ListAdminsInput) (*ListAdminsOutput, error) {
	return func(ctx context.Context, in *ListAdminsInput) (*ListAdminsOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.AdminListInstanceAdmins(ctx, generated.AdminListInstanceAdminsParams{
			Limit:  limit,
			Offset: in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListAdminsOutput{}
		out.Body.Items = make([]InstanceAdmin, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToInstanceAdmin(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// GrantAdmin handles POST /admin/instance-admins. Grants instance admin
// privileges to the user identified by the given public id.
func GrantAdmin(deps Deps) func(context.Context, *GrantAdminInput) (*GrantAdminOutput, error) {
	return func(ctx context.Context, in *GrantAdminInput) (*GrantAdminOutput, error) {
		actorID, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.Body.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceUserNotFound)
		}

		// Resolve internal user id.
		targetID, err := deps.Queries.AdminFindUserIdByPublicId(ctx, pid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.InstanceUserNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Check the user is not already an active admin.
		_, err = deps.Queries.AdminFindInstanceAdminByUserId(ctx, targetID)
		if err == nil {
			// Already an admin; treat as idempotent success.
			out := &GrantAdminOutput{}
			out.Body.Ok = true
			return out, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_, err = deps.Queries.AdminGrantInstanceAdmin(ctx, generated.AdminGrantInstanceAdminParams{
			PublicID:        types.New(),
			UserID:          targetID,
			GrantedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.instance_admin.grant",
			ActorID:      actorID,
			ResourceType: "user",
			ResourceID:   in.Body.UserID,
		})

		out := &GrantAdminOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// RevokeAdmin handles DELETE /admin/instance-admins/{userId}. Revokes
// instance admin privileges from the user identified by the given public id.
// Self-revocation and revoking the last remaining admin are rejected.
func RevokeAdmin(deps Deps) func(context.Context, *RevokeAdminInput) (*RevokeAdminOutput, error) {
	return func(ctx context.Context, in *RevokeAdminInput) (*RevokeAdminOutput, error) {
		actorID, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceAdminNotFound)
		}

		// Resolve internal user id.
		targetID, err := deps.Queries.AdminFindUserIdByPublicId(ctx, pid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.InstanceAdminNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Prevent self-revocation.
		if targetID == actorID {
			return nil, httpErr(apierrors.InstanceAdminSelfRevoke)
		}

		// Verify the target is actually an active admin.
		_, err = deps.Queries.AdminFindInstanceAdminByUserId(ctx, targetID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.InstanceAdminNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Guard against removing the last admin.
		count, err := deps.Queries.AdminCountActiveInstanceAdmins(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if count <= 1 {
			return nil, httpErr(apierrors.InstanceAdminLastAdmin)
		}

		err = deps.Queries.AdminRevokeInstanceAdmin(ctx, targetID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "admin.instance_admin.revoke",
			ActorID:      actorID,
			ResourceType: "user",
			ResourceID:   in.UserID,
		})

		out := &RevokeAdminOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
