// Package ai — metrics.go exposes workspace-scoped AI suggestion
// acceptance metrics (2.OBS-1). The numbers are derived from the
// append-only events log (`ai.suggestion.{proposed,applied,dismissed}`)
// so they stay consistent with the audit trail and survive restarts
// without a separate counter store.
package ai

import (
	"context"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// AiMetricsInput is the path+query input for GET /workspaces/{wsId}/ai/metrics.
type AiMetricsInput struct {
	WsID       string `path:"wsId"`
	WindowDays int    `query:"windowDays" minimum:"1" maximum:"365" default:"30" doc:"Trailing window in days"`
}

// AiMetricsOutputBody is the response payload for the metrics endpoint.
//
// acceptanceRate is applied / (applied + dismissed) over the window,
// clamped to [0, 1]. Proposed is reported separately because a
// suggestion may be neither applied nor dismissed yet (still pending).
type AiMetricsOutputBody struct {
	WindowDays     int     `json:"windowDays"`
	Proposed       int64   `json:"proposed"`
	Applied        int64   `json:"applied"`
	Dismissed      int64   `json:"dismissed"`
	AcceptanceRate float64 `json:"acceptanceRate" doc:"applied / (applied + dismissed), 0 when no decisions"`
}

// AiMetricsOutput is the Huma envelope for AiMetricsOutputBody.
type AiMetricsOutput struct {
	Body AiMetricsOutputBody
}

// Metrics handles GET /workspaces/{wsId}/ai/metrics.
func Metrics(deps Deps) func(context.Context, *AiMetricsInput) (*AiMetricsOutput, error) {
	return func(ctx context.Context, in *AiMetricsInput) (*AiMetricsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		window := in.WindowDays
		if window <= 0 {
			window = 30
		}
		since := time.Now().UTC().Add(-time.Duration(window) * 24 * time.Hour)
		row, err := deps.Queries.CountAiSuggestionOutcomesForWorkspace(ctx, generated.CountAiSuggestionOutcomesForWorkspaceParams{
			WorkspaceID: ws.ID,
			OccurredAt:  since,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		proposed := totalAsInt64(row.Proposed)
		applied := totalAsInt64(row.Applied)
		dismissed := totalAsInt64(row.Dismissed)
		decided := applied + dismissed
		var rate float64
		if decided > 0 {
			rate = float64(applied) / float64(decided)
		}
		return &AiMetricsOutput{Body: AiMetricsOutputBody{
			WindowDays:     window,
			Proposed:       proposed,
			Applied:        applied,
			Dismissed:      dismissed,
			AcceptanceRate: rate,
		}}, nil
	}
}
