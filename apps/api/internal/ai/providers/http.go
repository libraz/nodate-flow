package providers

import (
	"context"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/outbound"
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
// configure per-provider egress caps (4.SEC-2).
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

// ConfigureLimiter attaches a token-bucket limiter to the given
// destination key. Call from main at startup to enforce per-provider
// egress caps. Passing nil clears the slot.
func ConfigureLimiter(destination string, l outbound.RateLimiter) {
	outboundRegistry.Set(destination, l)
}

// OutboundSnapshot returns a copy of the per-destination limiter
// counters. Empty when no limiter has been configured. Surfaced via
// the AI metrics endpoint for ops dashboards (4.AGENT-2).
func OutboundSnapshot() map[string]outbound.LimiterStats {
	return outboundRegistry.Snapshot()
}

// doLimited runs req through sharedClient after waiting on the
// destination's outbound limiter. Unconfigured destinations are a
// no-op, so provider code can always funnel through this helper
// without conditional plumbing.
func doLimited(ctx context.Context, destination string, req *http.Request) (*http.Response, error) {
	if err := outboundRegistry.Wait(ctx, destination); err != nil {
		return nil, err
	}
	return sharedClient.Do(req)
}

// zero overwrites b with zero bytes. Used to scrub plaintext API keys after
// they have been written to an outbound Authorization header.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
