package ai

import (
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// mapProviderError converts an upstream LLM provider error into the
// canonical Huma problem+json envelope. Sentinel errors from the
// internal/ai/providers package are matched via errors.Is so handlers do
// not need to know about transport vs HTTP-status classification.
//
// For the rate-limit sentinel the upstream Retry-After value (if any)
// is extracted from the [providers.UpstreamError] wrapper and forwarded
// as a Retry-After response header so clients can back off the same way
// they would on a direct upstream call.
//
// Unknown errors fall through to AI.PROVIDER.UPSTREAM_UNREACHABLE (502)
// so callers always receive a recognised AI.* code rather than a bare
// 500 from the framework.
func mapProviderError(err error) error {
	switch {
	case errors.Is(err, providers.ErrUpstreamRateLimited):
		return handlerutil.HTTPErrWithRetryAfter(apierrors.AiProviderUpstreamRateLimited, retryAfterFrom(err))
	case errors.Is(err, providers.ErrUpstreamTimeout):
		return httpErr(apierrors.AiProviderUpstreamTimeout)
	case errors.Is(err, providers.ErrUpstreamAuthRejected):
		return httpErr(apierrors.AiProviderUpstreamAuthRejected)
	case errors.Is(err, providers.ErrUpstreamRequestRejected):
		return httpErr(apierrors.AiProviderUpstreamRequestRejected)
	case errors.Is(err, providers.ErrResponseInvalidJSON):
		return httpErr(apierrors.AiResponseInvalidJson)
	case errors.Is(err, providers.ErrResponseSchemaMismatch):
		return httpErr(apierrors.AiResponseSchemaMismatch)
	case errors.Is(err, providers.ErrUpstreamUnreachable):
		return httpErr(apierrors.AiProviderUpstreamUnreachable)
	default:
		return httpErr(apierrors.AiProviderUpstreamUnreachable)
	}
}

// retryAfterFrom returns the Retry-After value carried by a
// [providers.UpstreamError] wrapper, or "" when the error has no
// associated header.
func retryAfterFrom(err error) string {
	var ue *providers.UpstreamError
	if errors.As(err, &ue) {
		return ue.RetryAfter
	}
	return ""
}
