package providers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/outbound"
)

// defaultHTTPTimeout is the per-request upstream LLM timeout. Long enough
// for slow generations, short enough that a stuck call cannot pin a
// goroutine forever.
const defaultHTTPTimeout = 90 * time.Second

// dialTimeout bounds the TCP connect phase of an upstream call.
const dialTimeout = 10 * time.Second

// sharedClient is the package-wide *http.Client. We reuse one transport so
// keep-alives work; per-call timeouts come from the request context.
//
// The transport is spelled out rather than left to the default because of
// where the destination comes from: ai_providers.base_url is workspace
// admin input, so the dialer carries [safeControl] to refuse connects to
// non-public addresses, and the proxy is disabled — an HTTP_PROXY in the
// environment would otherwise route every call through a hop that the
// connect-time check never sees.
var sharedClient = &http.Client{
	Timeout: defaultHTTPTimeout,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
			Control:   safeControl,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// SetHTTPTimeoutForTest overrides the package-wide HTTP client timeout
// and returns a restore function. Only intended for tests that need to
// trigger the upstream-timeout sentinel without waiting the full 90s
// production deadline. Calling this from non-test code is undefined.
func SetHTTPTimeoutForTest(d time.Duration) func() {
	prev := sharedClient.Timeout
	sharedClient.Timeout = d
	return func() { sharedClient.Timeout = prev }
}

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

// wsLimiterIdleTTL is how long a workspace's egress limiter is kept
// after its last use. A workspace that has not made an LLM call in this
// long has a full bucket anyway, so dropping it changes nothing about
// what the next call is allowed to do.
//
// wsLimiterSweepEvery is how many creations pass between sweeps. The
// sweep walks the map, so tying it to creations rather than to calls
// keeps the cost proportional to how fast the map can grow.
const (
	wsLimiterIdleTTL    = 30 * time.Minute
	wsLimiterSweepEvery = 256
)

// wsLimiter is one workspace's egress bucket plus when it was last
// used, which is what lets an idle one be dropped.
type wsLimiter struct {
	limiter  *outbound.Limiter
	lastUsed atomic.Int64 // unix seconds
}

// wsLimiterStore holds per-workspace rate limiters keyed by workspace
// internal ID. Each entry is a token-bucket scoped to half the global
// per-provider rate, so a single tenant cannot starve other workspaces.
//
// Entries are evicted once idle: this map is process-lifetime state
// keyed by tenant, and an api process serving a long tail of workspaces
// would otherwise accumulate one bucket per workspace it had ever seen
// and never give any of them back.
type wsLimiterStore struct {
	mu        sync.RWMutex
	limiters  map[uint32]*wsLimiter
	rps       float64
	burst     int
	sinceLast int // creations since the last sweep
	now       func() time.Time
}

var wsStore = &wsLimiterStore{
	limiters: make(map[uint32]*wsLimiter),
}

// clock returns the store's time source.
func (s *wsLimiterStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
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
	now := wsStore.clock()
	wsStore.mu.RLock()
	if wsStore.rps <= 0 {
		wsStore.mu.RUnlock()
		return nil
	}
	entry := wsStore.limiters[wsID]
	wsStore.mu.RUnlock()
	if entry != nil {
		entry.lastUsed.Store(now.Unix())
		return entry.limiter
	}
	wsStore.mu.Lock()
	defer wsStore.mu.Unlock()
	// Double-check after acquiring write lock.
	if entry = wsStore.limiters[wsID]; entry != nil {
		entry.lastUsed.Store(now.Unix())
		return entry.limiter
	}
	entry = &wsLimiter{limiter: outbound.NewLimiter(wsStore.rps, wsStore.burst)}
	entry.lastUsed.Store(now.Unix())
	wsStore.limiters[wsID] = entry

	wsStore.sinceLast++
	if wsStore.sinceLast >= wsLimiterSweepEvery {
		wsStore.sinceLast = 0
		wsStore.evictIdleLocked(now)
	}
	return entry.limiter
}

// evictIdleLocked drops every workspace whose limiter has not been used
// within [wsLimiterIdleTTL]. Caller holds the write lock.
func (s *wsLimiterStore) evictIdleLocked(now time.Time) {
	cutoff := now.Add(-wsLimiterIdleTTL).Unix()
	for id, entry := range s.limiters {
		if entry.lastUsed.Load() < cutoff {
			delete(s.limiters, id)
		}
	}
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

	resp, err := sharedClient.Do(req) //#nosec G107 G704 -- request URL is configured by workspace admin via the AI provider settings, not derived from end-user input.
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

		resp, err = sharedClient.Do(req) //#nosec G107 G704 -- retry of the same admin-configured provider URL.
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// DestOpenAIEmbed is the limiter key for the embeddings endpoint. It is
// separate from [DestOpenAI] because the two have their own upstream
// quotas and a shared bucket would let a backfill of embeddings starve
// interactive completions.
const DestOpenAIEmbed = "embed.openai"

// DoLimited runs an already-built upstream request through the shared
// egress path: the SSRF-guarded dialer and proxy-free transport, the
// per-workspace and per-destination rate limiters, and the 429 retry.
//
// It is exported because the embeddings client lives in a sibling package
// and calls the same vendors from the same process. Its own bare
// http.Client was a second door out of the process that none of the caps
// could see, so a backfill could spend the provider's quota while the
// limiter reported a workspace well inside its budget, and a 429 came back
// to the caller as a hard failure instead of a retry.
func DoLimited(ctx context.Context, destination string, req *http.Request) (*http.Response, error) {
	return doLimited(ctx, destination, req)
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
	return time.Duration(1<<uint(attempt)) * time.Second //#nosec G115 -- attempt is the retry counter capped at maxRetries (single digit)
}

// zero overwrites b with zero bytes. Used to scrub plaintext API keys after
// they have been written to an outbound Authorization header.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
