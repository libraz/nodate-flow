package ai

import (
	"context"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// AiCostTodayInput is the path for GET /workspaces/{wsId}/ai/cost-today.
type AiCostTodayInput struct {
	WsID string `path:"wsId"`
}

// AiCostTodayOutputBody is the response payload for the cost-today endpoint.
//
// `costUsd` is today's accumulated LLM spend in USD (cents-from-DB / 100).
// `monthlyCapUsd` is omitted in the MVP (no env var defined yet); the field
// is reserved for the upcoming monthly cap once it lands.
// `date` is today in UTC as YYYY-MM-DD per docs/conventions/api-types.md.
type AiCostTodayOutputBody struct {
	CostUsd       float64  `json:"costUsd"`
	MonthlyCapUsd *float64 `json:"monthlyCapUsd,omitempty"`
	Date          string   `json:"date" doc:"UTC date (YYYY-MM-DD)"`
}

// AiCostTodayOutput is the Huma envelope for AiCostTodayOutputBody.
type AiCostTodayOutput struct {
	Body AiCostTodayOutputBody
}

// CostToday handles GET /workspaces/{wsId}/ai/cost-today.
func CostToday(deps Deps) func(context.Context, *AiCostTodayInput) (*AiCostTodayOutput, error) {
	return func(ctx context.Context, _ *AiCostTodayInput) (*AiCostTodayOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		now := time.Now().UTC()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		cents, err := deps.Queries.SumAiCostTodayForWorkspace(ctx, generated.SumAiCostTodayForWorkspaceParams{
			WorkspaceID: ws.ID,
			InvokedAt:   startOfDay,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &AiCostTodayOutput{Body: AiCostTodayOutputBody{
			CostUsd: float64(cents) / 100.0,
			Date:    now.Format("2006-01-02"),
		}}, nil
	}
}
