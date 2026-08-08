package ai

import (
	"context"
	"time"

	aicore "github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// CostTodayInput is the path for GET /workspaces/{wsId}/ai/cost-today.
type CostTodayInput struct {
	WsID string `path:"wsId"`
	Tz   string `query:"tz" doc:"Deprecated and ignored. The window is the workspace's own day, since that is the window the budget is enforced over."`
}

// CostTodayOutputBody is the response payload for the cost-today endpoint.
//
// `costUsd` is the LLM spend so far in the current budget window, in USD
// (cents-from-DB / 100).
// `monthlyCapUsd` is omitted in the MVP (no env var defined yet); the field
// is reserved for the upcoming monthly cap once it lands.
// `windowStartsAt` is when the window the figure covers began, as a unixtime
// in seconds per docs/conventions/api-types.md. It is the same instant the
// cost guard measures from, so a workspace that has been cut off can be
// reconciled against this response instead of guessed at.
// `date` is that window's day in the workspace timezone as YYYY-MM-DD.
type CostTodayOutputBody struct {
	CostUsd        float64  `json:"costUsd"`
	MonthlyCapUsd  *float64 `json:"monthlyCapUsd,omitempty"`
	WindowStartsAt int64    `json:"windowStartsAt" doc:"Start of the enforced budget window (unixtime seconds)."`
	Date           string   `json:"date" doc:"The window's date in the workspace timezone (YYYY-MM-DD)."`
}

// CostTodayOutput is the Huma envelope for CostTodayOutputBody.
type CostTodayOutput struct {
	Body CostTodayOutputBody
}

// CostToday handles GET /workspaces/{wsId}/ai/cost-today.
//
// The window is [ai.WorkspaceDayStart]: midnight in the workspace's own
// timezone. There is only one definition of "today" for AI spend because
// there is only one thing the answer is used for — telling an operator how
// close they are to the cap that is actually being enforced. This endpoint
// previously measured from midnight in a timezone the client asked for
// (flow-web sent the browser's), which meant the meter and the guard
// disagreed for most of the day in any workspace whose members were not in
// the workspace's zone: the panel could read $0.00 while every AI call was
// being refused. The `tz` parameter is kept so existing clients keep
// working and is ignored.
func CostToday(deps Deps) func(context.Context, *CostTodayInput) (*CostTodayOutput, error) {
	return func(ctx context.Context, _ *CostTodayInput) (*CostTodayOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		// The reported window comes off the same struct the sum was taken
		// with, so the figure and the window it claims to cover cannot
		// describe two different days.
		params := aicore.DailyCostParams(ctx, deps.Queries, ws.ID)
		cents, err := deps.Queries.SumAiCostTodayForWorkspace(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &CostTodayOutput{Body: costTodayBody(cents, params.InvokedAt)}, nil
	}
}

// costTodayBody renders the meter for a spend total and the window it was
// measured over. The date is formatted in the window's own location, so it
// names the day the operator's workspace is having rather than the day UTC
// is having.
func costTodayBody(cents int64, windowStart time.Time) CostTodayOutputBody {
	return CostTodayOutputBody{
		CostUsd:        float64(cents) / 100.0,
		WindowStartsAt: windowStart.Unix(),
		Date:           windowStart.Format("2006-01-02"),
	}
}
