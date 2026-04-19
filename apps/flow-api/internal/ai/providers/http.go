package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// doLimited runs req through sharedClient after waiting on the
// destination's outbound limiter. On HTTP 429, it retries with
// exponential backoff up to maxRetries times, honoring the
// Retry-After header when present.
func doLimited(ctx context.Context, destination string, req *http.Request) (*http.Response, error) {
	if err := outboundRegistry.Wait(ctx, destination); err != nil {
		return nil, err
	}

	resp, err := sharedClient.Do(req)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxRetries && resp.StatusCode == http.StatusTooManyRequests; attempt++ {
		// Read and discard body so connection can be reused.
		io.Copy(io.Discard, resp.Body)
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
