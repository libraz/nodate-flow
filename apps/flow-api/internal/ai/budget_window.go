package ai

import (
	"context"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// workspaceTimezoneLoader loads a workspace's IANA timezone by its internal
// id. *generated.Queries satisfies it; tests can supply a fake.
type workspaceTimezoneLoader interface {
	FindWorkspaceTimezoneCountryById(ctx context.Context, id uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error)
}

// DailyBudgetBoundary returns the start of the current day (local midnight) in
// the given IANA timezone, expressed as a time.Time suitable for the
// invoked_at lower bound of the daily AI-cost query.
//
// The per-workspace daily AI budget window must reset at the workspace's local
// midnight, not UTC midnight. Truncating "now" to UTC midnight would reset the
// cap at the wrong local hour for any non-UTC workspace (e.g. 09:00 in
// Asia/Tokyo). An empty or unrecognised zone name falls back to UTC so a
// misconfigured workspace still gets a coherent 24h window.
func DailyBudgetBoundary(now time.Time, tz string) time.Time {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// WorkspaceDayStart loads the workspace's timezone and returns the current
// local-midnight boundary for its daily AI budget window. A load error is
// swallowed to a UTC boundary: the meter must not fail the caller's request
// just because the timezone column could not be read.
func WorkspaceDayStart(ctx context.Context, loader workspaceTimezoneLoader, workspaceID uint32) time.Time {
	tz := ""
	if loader != nil {
		if row, err := loader.FindWorkspaceTimezoneCountryById(ctx, workspaceID); err == nil {
			tz = row.Timezone
		}
	}
	return DailyBudgetBoundary(time.Now(), tz)
}
