package ai

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// workspaceTimezoneLoader loads a workspace's IANA timezone by its internal
// id. *generated.Queries satisfies it; tests can supply a fake.
type workspaceTimezoneLoader interface {
	FindWorkspaceTimezoneCountryById(ctx context.Context, id uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error)
}

// nowFunc is the clock the window boundary is computed from. It is a
// variable so a test can compare two boundaries taken from the same
// instant; the alternative is comparing two separate reads of the wall
// clock, which agree except across a midnight and therefore describe a
// test that is right almost always.
var nowFunc = time.Now

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
	z, err := region.Resolve(tz)
	if err != nil {
		z = region.UTC()
	}
	return region.DayOf(now, z).Start(z)
}

// WorkspaceDayStart loads the workspace's timezone and returns the current
// local-midnight boundary for its daily AI budget window. A load error is
// swallowed to a UTC boundary: the meter must not fail the caller's request
// just because the timezone column could not be read.
//
// This is the only definition of "today" for AI spend, and every place that
// bounds a cost query — the completion guard, the embed guard, the
// cost-today meter — has to use it. A guard computing its own boundary
// picks a defensible one — the workspace's local midnight, UTC midnight,
// the timezone the browser reported — and any two that pick differently
// disagree about when the budget resets: a workspace is refused every AI
// call for hours after its reset while the meter reports that nothing has
// been spent, and neither answer is wrong on its own terms.
//
// Call sites do not use it directly, though: they take a whole params
// struct from [DailyCostParams] or [DailyEmbedCostParams], so there is no
// place left to write a boundary by hand.
func WorkspaceDayStart(ctx context.Context, loader workspaceTimezoneLoader, workspaceID uint32) time.Time {
	tz := ""
	if loader != nil {
		if row, err := loader.FindWorkspaceTimezoneCountryById(ctx, workspaceID); err == nil {
			tz = row.Timezone
		}
	}
	return DailyBudgetBoundary(nowFunc(), tz)
}

// DailyCostParams builds the bounded query for what a workspace has spent
// on LLM calls in the current budget window.
//
// The whole parameter struct is built here rather than the boundary alone
// because the failure mode is not a wrong calculation — a call site
// computing its own boundary computes a defensible one — it is call sites
// each computing their own. Handing back the finished struct leaves the
// caller nothing to decide, and [TestCostQueryParamsAreCentralized] keeps
// the literal from being written anywhere else.
func DailyCostParams(ctx context.Context, loader workspaceTimezoneLoader, workspaceID uint32) generated.SumAiCostTodayForWorkspaceParams {
	return generated.SumAiCostTodayForWorkspaceParams{
		WorkspaceID: workspaceID,
		InvokedAt:   WorkspaceDayStart(ctx, loader, workspaceID),
	}
}

// DailyEmbedCostParams is [DailyCostParams] for the separate embedding
// budget. The two totals are metered against different caps but over the
// same day: an embed budget that resets on its own schedule blocks
// indexing for hours after the chat budget has reset, with no meter
// anywhere showing why.
func DailyEmbedCostParams(ctx context.Context, loader workspaceTimezoneLoader, workspaceID uint32) generated.SumEmbedCostTodayForWorkspaceParams {
	return generated.SumEmbedCostTodayForWorkspaceParams{
		WorkspaceID: workspaceID,
		InvokedAt:   WorkspaceDayStart(ctx, loader, workspaceID),
	}
}
