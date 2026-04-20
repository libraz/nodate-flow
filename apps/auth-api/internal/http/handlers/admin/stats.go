package admin

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// InstanceStats handles GET /admin/instance-stats. Returns high-level counts
// for active users and workspaces across the entire instance.
func InstanceStats(deps Deps) func(context.Context, *struct{}) (*InstanceStatsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*InstanceStatsOutput, error) {
		totalUsers, err := deps.Queries.CountActiveUsers(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		totalWorkspaces, err := deps.Queries.CountActiveWorkspaces(ctx)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &InstanceStatsOutput{}
		out.Body.TotalUsers = totalUsers
		out.Body.TotalWorkspaces = totalWorkspaces
		return out, nil
	}
}
