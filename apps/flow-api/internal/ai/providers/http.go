package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/outbound"
)

// defaultHTTPTimeout is the per-request upstream LLM timeout. Long enough
// for slow generations, short enough that a stuck call cannot pin a
// goroutine forever.
const defaultHTTPTimeout = 90 * time.Second

// sharedClient is the package-wide *http.Client. We reuse one transport so
// keep-alives work; per-call timeouts come from the request context.
var sharedClient = &http.Client{Timeout: defaultHTTPTimeout}

// Destination constants for the outbound rate limiter registry.
// Each provider routes its Do() through these keys so operators can
// configure per-provider egress caps.
const (
	DestAnthropic = "llm.anthropic"
	DestOpenAI    = "llm.openai"
	DestGoogle    = "llm.google"
	DestOllama    = "llm.ollama"
)

// outboundRegistry is the package-wide egress limiter registry. It is
// empty by default (fail-open) until wired from main via
// [ConfigureLimiter].
var outboundRegistry = outbound.NewRegistry()

// --------------------------------------------------------------------------
// Per-workspace egress rate limiting
// --------------------------------------------------------------------------

type wsCtxKey struct{}

// WithWorkspaceID returns a copy of ctx tagged with the internal
// workspace ID. The Orchestrator sets this before calling
// prov.Complete so doLimited can enforce per-workspace egress caps
// without coupling the Provider interface to workspace semantics.
func WithWorkspaceID(ctx context.Context, workspaceID uint32) context.Context {
	if workspaceID == 0 {
		return ctx
	}
	return context.WithValue(ctx, wsCtxKey{}, workspaceID)
}

// WorkspaceIDFromContext returns the workspace ID previously set via
// WithWorkspaceID, or zero when the context was never tagged.
func WorkspaceIDFromContext(ctx context.Context) uint32 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(wsCtxKey{}).(uint32)
	return v
}

// wsLimiterStore holds per-workspace rate limiters keyed by workspace
// internal ID. Each entry is a token-bucket scoped to half the global
// per-provider rate, so a single tenant cannot starve other workspaces.
type wsLimiterStore struct {
	mu       sync.RWMutex
	limiters map[uint32]*outbound.Limiter
	rps      float64
	burst    int
}

var wsStore = &wsLimiterStore{
	limiters: make(map[uint32]*outbound.Limiter),
}

// ConfigureWorkspaceLimiter sets the per-workspace rate and burst.
// Typically called from main right after configuring the global
// limiter, with rps = global_rps/2 and burst = max(1, global_burst/2).
func ConfigureWorkspaceLimiter(rps float64, burst int) {
	if rps <= 0 {
		return
	}
	if burst <= 0 {
		burst = 1
	}
	wsStore.mu.Lock()
	wsStore.rps = rps
	wsStore.burst = burst
	wsStore.mu.Unlock()
}

// getOrCreateWSLimiter returns the rate limiter for the given workspace,
// creating one lazily if it does not exist. Returns nil when
// per-workspace limiting is not configured.
func getOrCreateWSLimiter(wsID uint32) *outbound.Limiter {
	if wsID == 0 {
		return nil
	}
	wsStore.mu.RLock()
	if wsStore.rps <= 0 {
		wsStore.mu.RUnlock()
		return nil
	}
	l := wsStore.limiters[wsID]
	wsStore.mu.RUnlock()
	if l != nil {
		return l
	}
	wsStore.mu.Lock()
	defer wsStore.mu.Unlock()
	// Double-check after acquiring write lock.
	if l = wsStore.limiters[wsID]; l != nil {
		return l
	}
	l = outbound.NewLimiter(wsStore.rps, wsStore.burst)
	wsStore.limiters[wsID] = l
	return l
}

// ConfigureLimiter attaches a token-bucket limiter to the given
// destination key. Call from main at startup to enforce per-provider
// egress caps. Passing nil clears the slot.
func ConfigureLimiter(destination string, l outbound.RateLimiter) {
	outboundRegistry.Set(destination, l)
}

// OutboundSnapshot returns a copy of the per-destination limiter
// counters. Empty when no limiter has been configured. Surfaced via
// the AI metrics endpoint for ops dashboards.
func OutboundSnapshot() map[string]outbound.LimiterStats {
	return outboundRegistry.Snapshot()
}

// maxRetries is the number of additional attempts after the first 429.
const maxRetries = 3

// doLimited runs req through sharedClient after waiting on both the
// per-workspace limiter (if a workspace ID is on the context) and the
// global per-destination outbound limiter. The workspace limiter is
// checked first so a single tenant cannot exhaust the global quota.
// On HTTP 429, it retries with exponential backoff up to maxRetries
// times, honoring the Retry-After header when present.
func doLimited(ctx context.Context, destination string, req *http.Request) (*http.Response, error) {
	// Per-workspace egress cap (checked first).
	if wsID := WorkspaceIDFromContext(ctx); wsID != 0 {
		if wl := getOrCreateWSLimiter(wsID); wl != nil {
			if err := wl.Wait(ctx); err != nil {
				return nil, fmt.Errorf("workspace rate limit: %w", err)
			}
		}
	}
	// Global per-destination cap.
	if err := outboundRegistry.Wait(ctx, destination); err != nil {
		return nil, err
	}

	resp, err := sharedClient.Do(req)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxRetries && resp.StatusCode == http.StatusTooManyRequests; attempt++ {
		// Read and discard body so connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Parse Retry-After header (seconds) or use exponential backoff.
		wait := retryDelay(resp, attempt)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		// Re-wait on rate limiter before retrying.
		if err := outboundRegistry.Wait(ctx, destination); err != nil {
			return nil, err
		}

		// Clone request body for retry (if possible).
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("retry body: %w", err)
			}
			req.Body = body
		}

		resp, err = sharedClient.Do(req)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// retryDelay computes the wait duration for a 429 retry. It uses the
// Retry-After header if present and numeric, otherwise falls back to
// exponential backoff: 1s, 2s, 4s.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 && secs <= 120 {
			return time.Duration(secs) * time.Second
		}
	}
	// Exponential backoff: 1s, 2s, 4s.
	return time.Duration(1<<uint(attempt)) * time.Second
}

// zero overwrites b with zero bytes. Used to scrub plaintext API keys after
// they have been written to an outbound Authorization header.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
