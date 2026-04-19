package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMetricsNotOnMainPort verifies that the Prometheus /metrics endpoint
// is NOT available on the main API port. Metrics are served on a separate
// internal-only listener (NF_FLOW_METRICS_PORT, default 9090) so they
// cannot be reached through the public API.
func TestMetricsNotOnMainPort(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, testServerURL+"/metrics", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The main router does not mount /metrics, so it should return 404.
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"/metrics must not be reachable on the main API port")
}
