package duedate

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// ZoneLoader reads the stored timezone tiers. *generated.Queries
// satisfies it.
type ZoneLoader interface {
	FindUserProfileById(ctx context.Context, id uint32) (generated.FindUserProfileByIdRow, error)
	FindWorkspaceTimezoneCountryById(ctx context.Context, id uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error)
}

// UserZone returns the zone a user's "today" is read in: their own
// preference, else the workspace's, else UTC. A failed lookup or a
// stored name the zoneinfo database does not know falls through to the
// next tier, so a missing profile row degrades to the workspace zone
// instead of failing the request.
func UserZone(ctx context.Context, q ZoneLoader, workspaceID, userID uint32) region.Zone {
	var userTz string
	if profile, err := q.FindUserProfileById(ctx, userID); err == nil {
		userTz = profile.Timezone
	}
	return resolve(userTz, workspaceTz(ctx, q, workspaceID))
}

// WorkspaceZone returns the zone a workspace's "today" is read in, for
// passes that run for no particular user: the workspace's timezone, else
// UTC.
func WorkspaceZone(ctx context.Context, q ZoneLoader, workspaceID uint32) region.Zone {
	return resolve("", workspaceTz(ctx, q, workspaceID))
}

func workspaceTz(ctx context.Context, q ZoneLoader, workspaceID uint32) string {
	row, err := q.FindWorkspaceTimezoneCountryById(ctx, workspaceID)
	if err != nil {
		return ""
	}
	return row.Timezone
}

// resolve drops candidates the zoneinfo database does not know, so the
// tiers below them still apply, and resolves the rest most-specific
// first.
func resolve(candidates ...string) region.Zone {
	for i, c := range candidates {
		if region.ValidateTimezone(c) != nil {
			candidates[i] = ""
		}
	}
	z, err := region.Resolve(candidates...)
	if err != nil {
		return region.UTC()
	}
	return z
}
