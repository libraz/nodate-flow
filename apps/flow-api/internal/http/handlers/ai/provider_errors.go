package ai

import (
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
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
	// The stored key could not be unsealed, so nothing was sent. Reported
	// as an unreachable provider this is the most misleading answer the
	// surface can give: the network is fine, every provider in the
	// deployment is failing the same way, and retrying never helps. The
	// cause is that NF_SECRET_KEY no longer matches the ciphertext, and
	// that is what the caller has to be told.
	case errors.Is(err, providers.ErrKeyDecryptFailed):
		return httpErr(apierrors.AiProviderKeyDecryptFailed)
	// The provider row cannot be turned into a client at all: a base URL
	// on a kind that does not take one, a kind nothing implements, a
	// missing key. Nothing was sent upstream, so reporting "could not
	// reach the provider" points the reader at the network and at the
	// vendor's status page — the two places the answer is not. The
	// configuration is what has to change, which is what 412 says.
	// The endpoint itself is the problem: it names an address this
	// deployment will not contact, or it is not a usable http(s) URL at
	// all. This has its own code because the alternative — reporting it as
	// an unreachable provider — sends the reader to the network and the
	// vendor's status page, and the refusal came from us.
	case errors.Is(err, providers.ErrBaseURLDestinationNotAllowed),
		errors.Is(err, providers.ErrInvalidBaseURL):
		return httpErr(apierrors.AiProviderBaseUrlNotAllowed)
	case errors.Is(err, providers.ErrBaseURLNotAllowed),
		errors.Is(err, providers.ErrUnknownKind),
		errors.Is(err, providers.ErrMissingKey):
		return httpErr(apierrors.AiProviderNotConfigured)
	default:
		return httpErr(apierrors.AiProviderUpstreamUnreachable)
	}
}

// createProviderError maps a rejected provider configuration to the code
// the admin sees on submit.
//
// The endpoint cases get their own code rather than the generic invalid-
// field answer: a refused base URL is the one rejection here whose cause
// is a deployment policy rather than a typo, and an admin who is told only
// that a field is invalid has no way to learn that the address was
// deliberately refused or that there is a setting which permits it.
func createProviderError(err error) error {
	switch {
	case errors.Is(err, providers.ErrBaseURLDestinationNotAllowed),
		errors.Is(err, providers.ErrInvalidBaseURL):
		return httpErr(apierrors.AiProviderBaseUrlNotAllowed)
	default:
		return httpErr(apierrors.ValidationBodyFieldInvalid)
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
