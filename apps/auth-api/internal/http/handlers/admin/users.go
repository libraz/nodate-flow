package admin

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
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
			Column1:  in.Search,
			CONCAT:   in.Search,
			CONCAT_2: in.Search,
			Column4:  filterEnabled,
			Enabled:  enabledBool,
			Limit:    limit,
			Offset:   in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListUsersOutput{}
		out.Body.Items = make([]AdminUser, len(rows))
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

		if *in.Body.Enabled {
			err = deps.Queries.AdminEnableUser(ctx, pid)
		} else {
			err = deps.Queries.AdminSuspendUser(ctx, pid)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
