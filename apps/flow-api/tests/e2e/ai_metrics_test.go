package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAiMetricsExposesOutboundLimits hits GET /workspaces/{wsId}/ai/metrics
// and asserts the response payload includes the new `outboundLimits`
// field added in 4.AGENT-2. The default test server does not configure
// any limiters, so the slice must be present and empty (not null).
func TestAiMetricsExposesOutboundLimits(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var body struct {
		WindowDays     int     `json:"windowDays"`
		Proposed       int64   `json:"proposed"`
		Applied        int64   `json:"applied"`
		Dismissed      int64   `json:"dismissed"`
		AcceptanceRate float64 `json:"acceptanceRate"`
		OutboundLimits []struct {
			Destination string `json:"destination"`
			Allowed     uint64 `json:"allowed"`
			Waited      uint64 `json:"waited"`
			Denied      uint64 `json:"denied"`
		} `json:"outboundLimits"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/metrics",
		tt.AccessToken, nil, &body)

	require.Equal(t, 30, body.WindowDays)
	require.NotNil(t, body.OutboundLimits)
}
