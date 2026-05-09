// Package ai — metrics.go exposes workspace-scoped AI suggestion
// acceptance metrics. The numbers are derived from the
// append-only events log (`ai.suggestion.{proposed,applied,dismissed}`)
// so they stay consistent with the audit trail and survive restarts
// without a separate counter store.
package ai

import (
	"context"
	"sort"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// OutboundLimitStat is the wire shape for one egress limiter's
// counters. Mirrors outbound.LimiterStats so the http surface stays
// independent of the internal package.
type OutboundLimitStat struct {
	Destination string `json:"destination"`
	Allowed     uint64 `json:"allowed"`
	Waited      uint64 `json:"waited"`
	Denied      uint64 `json:"denied"`
}

// MetricsInput is the path+query input for GET /workspaces/{wsId}/ai/metrics.
type MetricsInput struct {
	WsID       string `path:"wsId"`
	WindowDays int    `query:"windowDays" minimum:"1" maximum:"365" default:"30" doc:"Trailing window in days"`
}

// AiMetricsOutputBody is the response payload for the metrics endpoint.
//
// acceptanceRate is applied / (applied + dismissed) over the window,
// clamped to [0, 1]. Proposed is reported separately because a
// suggestion may be neither applied nor dismissed yet (still pending).
type AiMetricsOutputBody struct {
	WindowDays     int                 `json:"windowDays"`
	Proposed       int64               `json:"proposed"`
	Applied        int64               `json:"applied"`
	Dismissed      int64               `json:"dismissed"`
	AcceptanceRate float64             `json:"acceptanceRate" doc:"applied / (applied + dismissed), 0 when no decisions"`
	OutboundLimits []OutboundLimitStat `json:"outboundLimits" doc:"Per-provider egress rate limiter counters"`
}

// MetricsOutput is the Huma envelope for AiMetricsOutputBody.
type MetricsOutput struct {
	Body AiMetricsOutputBody
}

// Metrics handles GET /workspaces/{wsId}/ai/metrics.
func Metrics(deps Deps) func(context.Context, *MetricsInput) (*MetricsOutput, error) {
	return func(ctx context.Context, in *MetricsInput) (*MetricsOutput, error) {
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
		snap := providers.OutboundSnapshot()
		limits := make([]OutboundLimitStat, 0, len(snap))
		for dest, s := range snap {
			limits = append(limits, OutboundLimitStat{
				Destination: dest,
				Allowed:     s.Allowed,
				Waited:      s.Waited,
				Denied:      s.Denied,
			})
		}
		sortOutboundLimits(limits)
		return &MetricsOutput{Body: AiMetricsOutputBody{
			WindowDays:     window,
			Proposed:       proposed,
			Applied:        applied,
			Dismissed:      dismissed,
			AcceptanceRate: rate,
			OutboundLimits: limits,
		}}, nil
	}
}

// sortOutboundLimits orders limits deterministically by destination so
// the response is stable across requests (snapshot iteration order is
// non-deterministic).
func sortOutboundLimits(s []OutboundLimitStat) {
	sort.Slice(s, func(i, j int) bool { return s[i].Destination < s[j].Destination })
}
