package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Sentinel errors returned by Provider.Complete implementations so callers
// can map upstream failure modes to precise public error codes
// (AI.PROVIDER.* / AI.RESPONSE.*) without parsing string messages.
//
// Implementations MUST wrap one of these sentinels via %w when returning
// an error so that errors.Is(err, providers.ErrXxx) keeps working through
// the call chain (orchestrator, nlquery WorkspaceProvider, etc.).
var (
	// ErrUpstreamUnreachable is returned when the request to the upstream
	// LLM provider failed at the transport layer (DNS, TCP, TLS, connection
	// refused or reset). Maps to AI.PROVIDER.UPSTREAM_UNREACHABLE (502).
	ErrUpstreamUnreachable = errors.New("ai/providers: upstream unreachable")

	// ErrUpstreamTimeout is returned when the upstream did not respond
	// within the configured deadline. Maps to AI.PROVIDER.UPSTREAM_TIMEOUT
	// (504).
	ErrUpstreamTimeout = errors.New("ai/providers: upstream timeout")

	// ErrUpstreamRateLimited is returned when the upstream responded with
	// HTTP 429 after the client exhausted its retry budget. Maps to
	// AI.PROVIDER.UPSTREAM_RATE_LIMITED (429).
	ErrUpstreamRateLimited = errors.New("ai/providers: upstream rate limited")

	// ErrUpstreamAuthRejected is returned when the upstream responded with
	// HTTP 401 or 403. Maps to AI.PROVIDER.UPSTREAM_AUTH_REJECTED (502).
	ErrUpstreamAuthRejected = errors.New("ai/providers: upstream auth rejected")

	// ErrUpstreamRequestRejected is returned when the upstream responded
	// with a non-success status that is not auth-related, not rate-limited,
	// and not handled by another sentinel (4xx or 5xx). Maps to
	// AI.PROVIDER.UPSTREAM_REQUEST_REJECTED (502).
	ErrUpstreamRequestRejected = errors.New("ai/providers: upstream request rejected")

	// ErrResponseInvalidJSON is returned when the upstream response body
	// could not be decoded as JSON. Maps to AI.RESPONSE.INVALID_JSON (502).
	ErrResponseInvalidJSON = errors.New("ai/providers: upstream response is not valid json")

	// ErrResponseSchemaMismatch is returned when the upstream JSON decoded
	// successfully but failed structural validation (missing required
	// fields, wrong types). Maps to AI.RESPONSE.SCHEMA_MISMATCH (502).
	ErrResponseSchemaMismatch = errors.New("ai/providers: upstream response did not match schema")
)

// UpstreamError carries optional metadata (HTTP status, Retry-After
// header) alongside one of the sentinel errors above. Callers may
// type-assert via errors.As to propagate Retry-After to the client; the
// sentinel is always reachable via errors.Is.
type UpstreamError struct {
	Sentinel   error
	Status     int
	RetryAfter string
}

// Error implements error.
func (e *UpstreamError) Error() string {
	if e == nil || e.Sentinel == nil {
		return "ai/providers: upstream error"
	}
	if e.Status > 0 {
		return fmt.Sprintf("%s (status=%d)", e.Sentinel.Error(), e.Status)
	}
	return e.Sentinel.Error()
}

// Unwrap returns the sentinel so errors.Is(err, providers.ErrXxx) works.
func (e *UpstreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Sentinel
}

// classifyTransportError maps a transport-layer error from sharedClient.Do
// to the appropriate sentinel. Context deadlines and net.Error timeouts
// become ErrUpstreamTimeout; everything else is ErrUpstreamUnreachable.
func classifyTransportError(ctx context.Context, err error) *UpstreamError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &UpstreamError{Sentinel: ErrUpstreamTimeout}
	}
	if ctx != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &UpstreamError{Sentinel: ErrUpstreamTimeout}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &UpstreamError{Sentinel: ErrUpstreamTimeout}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return &UpstreamError{Sentinel: ErrUpstreamTimeout}
	}
	if strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded") {
		return &UpstreamError{Sentinel: ErrUpstreamTimeout}
	}
	return &UpstreamError{Sentinel: ErrUpstreamUnreachable}
}

// classifyHTTPStatus maps an upstream HTTP status code to the appropriate
// sentinel. Returns nil for 2xx so callers can treat success as "no
// classified error".
func classifyHTTPStatus(status int, retryAfter string) *UpstreamError {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 401 || status == 403:
		return &UpstreamError{Sentinel: ErrUpstreamAuthRejected, Status: status}
	case status == 429:
		return &UpstreamError{Sentinel: ErrUpstreamRateLimited, Status: status, RetryAfter: retryAfter}
	default:
		return &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: status}
	}
}
