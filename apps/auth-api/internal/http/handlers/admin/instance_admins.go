package admin

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
)

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("admin: answered")

// instanceAdmins is the owner set of the instance: active grants whose user
// is enabled. A suspended admin cannot sign in, so it does not count toward
// the admins that remain.
func instanceAdmins() memberkit.OwnerSet {
	return memberkit.OwnerSet{
		Query: `SELECT ia.user_id FROM instance_admins ia
		        INNER JOIN users u ON u.id = ia.user_id AND u.enabled = TRUE
		        WHERE ia.enabled = TRUE AND ia.revoked_at IS NULL`,
	}
}

// ensureAdminRemains answers INSTANCE.ADMIN.LAST_ADMIN unless an enabled
// admin other than leaving remains. It locks the admin set, so it must run
// in the transaction that then revokes or suspends leaving.
func ensureAdminRemains(ctx context.Context, tx *dbretry.Tx, leaving uint32) (*apierr.Spec, error) {
	err := memberkit.EnsureOwnerRemains(ctx, tx, instanceAdmins(), leaving)
	if errors.Is(err, memberkit.ErrLastOwner) {
		return apierrors.InstanceAdminLastAdmin, nil
	}
	return nil, err
}

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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceUserNotFound, apierrors.InternalUnexpected))
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
			GrantedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceAdminNotFound, apierrors.InternalUnexpected))
		}

		// Prevent self-revocation.
		if targetID == actorID {
			return nil, httpErr(apierrors.InstanceAdminSelfRevoke)
		}

		// Verify the target is actually an active admin.
		_, err = deps.Queries.AdminFindInstanceAdminByUserId(ctx, targetID)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceAdminNotFound, apierrors.InternalUnexpected))
		}

		// The last-admin guard and the revoke share a transaction that locks
		// the admin set, so two admins revoking each other at once cannot
		// both pass it. Another path may also have revoked the grant since
		// the check above, in which case the statement matches nothing: that
		// answers as if the grant were never there and records no audit
		// entry, so the trail never claims a revocation that did not happen.
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "admin.RevokeAdmin", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			if spec, err := ensureAdminRemains(ctx, tx, targetID); err != nil || spec != nil {
				if spec != nil {
					answered = httpErr(spec)
					return errAnswered
				}
				return err
			}
			affected, err := generated.New(tx).AdminRevokeInstanceAdmin(ctx, targetID)
			if err != nil {
				return err
			}
			if affected == 0 {
				answered = httpErr(apierrors.InstanceAdminNotFound)
				return errAnswered
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
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
