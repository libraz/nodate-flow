package admin

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
)

// ListUsers handles GET /admin/users. Returns a paginated list of all users
// with optional search and enabled-status filtering.
func ListUsers(deps Deps) func(context.Context, *ListUsersInput) (*ListUsersOutput, error) {
	return func(ctx context.Context, in *ListUsersInput) (*ListUsersOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		var filterEnabled interface{}
		var enabledBool bool
		if in.Enabled != "" {
			enabledBool = in.Enabled == "true"
			filterEnabled = enabledBool
		}

		rows, err := deps.Queries.AdminListUsers(ctx, generated.AdminListUsersParams{
			Column1: in.Search,
			CONCAT:  in.Search,
			Column4: filterEnabled,
			Enabled: enabledBool,
			Limit:   limit,
			Offset:  in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListUsersOutput{}
		out.Body.Items = make([]User, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = rowToAdminUser(r)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// GetUser handles GET /admin/users/{userId}. Returns the detail view for a
// single user identified by public id.
func GetUser(deps Deps) func(context.Context, *GetUserInput) (*GetUserOutput, error) {
	return func(ctx context.Context, in *GetUserInput) (*GetUserOutput, error) {
		pid, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceUserNotFound)
		}

		row, err := deps.Queries.AdminGetUser(ctx, pid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceUserNotFound, apierrors.InternalUnexpected))
		}

		return &GetUserOutput{Body: rowToAdminUserDetail(row)}, nil
	}
}

// PatchUser handles PATCH /admin/users/{userId}. Currently supports toggling
// the enabled flag to suspend or re-enable a user account.
func PatchUser(deps Deps) func(context.Context, *PatchUserInput) (*PatchUserOutput, error) {
	return func(ctx context.Context, in *PatchUserInput) (*PatchUserOutput, error) {
		uid, _ := authn.ActorFromContext(ctx)

		pid, err := types.Parse(in.UserID)
		if err != nil {
			return nil, httpErr(apierrors.InstanceUserNotFound)
		}

		if in.Body.Enabled == nil {
			out := &PatchUserOutput{}
			out.Body.Ok = true
			return out, nil
		}

		// Suspending is held to the rules revoking is: an admin cannot lock
		// themselves out, and the instance keeps at least one enabled admin.
		// The guard locks the admin set in the transaction that suspends.
		var targetID uint32
		if !*in.Body.Enabled {
			targetID, err = deps.Queries.AdminFindUserIdByPublicId(ctx, pid)
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.InstanceUserNotFound, apierrors.InternalUnexpected))
			}
			if targetID == uid {
				return nil, httpErr(apierrors.InstanceUserSelfSuspend)
			}
		}

		// Both statements guard on the current state, so a zero count is
		// either "no such user" or "already in the requested state". The
		// second is not an error — PATCH is idempotent — so tell them
		// apart with a read rather than answering 404 for both. Without
		// the check, suspending a user id that does not exist reported
		// success and wrote an audit entry saying an account had been
		// suspended, which is the record an incident review would read.
		var (
			rows     int64
			answered error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "admin.PatchUser", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			q := generated.New(tx)
			var err error
			if *in.Body.Enabled {
				rows, err = q.AdminEnableUser(ctx, pid)
				return err
			}
			spec, err := ensureAdminRemains(ctx, tx, targetID)
			if err != nil {
				return err
			}
			if spec != nil {
				answered = httpErr(spec)
				return errAnswered
			}
			rows, err = q.AdminSuspendUser(ctx, pid)
			return err
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			if _, getErr := deps.Queries.AdminGetUser(ctx, pid); getErr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(getErr, apierrors.InstanceUserNotFound, apierrors.InternalUnexpected))
			}
			// The user exists and already holds the requested state.
			// Nothing changed, so nothing is recorded.
			out := &PatchUserOutput{}
			out.Body.Ok = true
			return out, nil
		}

		action := "admin.user.enable"
		if !*in.Body.Enabled {
			action = "admin.user.suspend"
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       action,
			ActorID:      uid,
			ResourceType: "user",
			ResourceID:   in.UserID,
		})

		out := &PatchUserOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
