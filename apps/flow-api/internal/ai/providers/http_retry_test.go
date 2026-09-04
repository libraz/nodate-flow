package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A 429 retry is another request on the wire, so it has to cost the
// workspace what the first attempt cost. Charging only the first lets a
// tenant in a sustained 429 loop issue maxRetries+1 upstream calls per
// token it holds, so the per-workspace egress cap under-counts by that
// factor — precisely while the upstream is asking for less traffic.
//
// The test swaps the process-wide limiter store, so it does not run in
// parallel.
func TestRetryChargesTheWorkspacePerAttempt(t *testing.T) {
	withWSLimiterStore(t, nil)

	var attempts atomic.Uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			// Shortest delay retryDelay honours, so the retry is
			// observed without the exponential fallback's 1s floor
			// growing with every attempt.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const wsID = 7
	ctx := WithWorkspaceID(context.Background(), wsID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := doLimited(ctx, "llm.test", req)
	if err != nil {
		t.Fatalf("doLimited: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	sent := attempts.Load()
	if sent != 2 {
		t.Fatalf("upstream attempts = %d, want 2 (the 429 and its retry)", sent)
	}

	limiter := getOrCreateWSLimiter(wsID)
	if limiter == nil {
		t.Fatal("workspace has no egress limiter")
	}
	stats := limiter.Stats()
	charged := stats.Allowed + stats.Waited
	if charged != sent {
		t.Fatalf("workspace charged %d tokens for %d upstream attempts; the retry was free", charged, sent)
	}
}
