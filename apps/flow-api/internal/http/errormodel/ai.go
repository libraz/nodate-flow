package errormodel

import (
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// AIFailure maps an error from the AI layer to the catalog code every
// transport answers with, plus the upstream Retry-After value when the
// provider supplied one ("" otherwise).
//
// It is the only mapping for this failure family: REST handlers pass the
// pair straight to handlerutil.HTTPErrWithRetryAfter, and MCP tools wrap
// the spec with apierrors.Wrap, so the same failure reads the same on
// every surface. Anything unrecognised is reported as an unreachable
// provider.
func AIFailure(err error) (*apierr.Spec, string) {
	switch {
	case errors.Is(err, ai.ErrNoProvider),
		errors.Is(err, providers.ErrNoEnabledProvider):
		return apierrors.AiProviderNotConfigured, ""
	case errors.Is(err, ai.ErrDailyBudgetExceeded),
		errors.Is(err, nlquery.ErrBudgetExceeded),
		errors.Is(err, nlconstraint.ErrBudgetExceeded),
		errors.Is(err, nlcommand.ErrBudgetExceeded):
		return apierrors.AiCostGuardExceeded, ""
	case errors.Is(err, ai.ErrAgentCostCapExceeded):
		return apierrors.WsTaskAgentCostCapExceeded, ""
	case errors.Is(err, ai.ErrAgentPaused):
		return apierrors.AiAgentPaused, ""
	// The lens compiler has its own code because the client answers it
	// with a "rephrase" prompt rather than a generic failure.
	case errors.Is(err, nlquery.ErrUnparseable):
		return apierrors.AiNlQueryUnparseable, ""
	case errors.Is(err, ai.ErrParse),
		errors.Is(err, nlconstraint.ErrUnparseable),
		errors.Is(err, nlcommand.ErrUnresolvable),
		errors.Is(err, providers.ErrResponseInvalidJSON):
		return apierrors.AiResponseInvalidJson, ""
	case errors.Is(err, providers.ErrResponseSchemaMismatch):
		return apierrors.AiResponseSchemaMismatch, ""
	case errors.Is(err, providers.ErrUpstreamRateLimited):
		return apierrors.AiProviderUpstreamRateLimited, retryAfterFrom(err)
	case errors.Is(err, providers.ErrUpstreamTimeout):
		return apierrors.AiProviderUpstreamTimeout, ""
	case errors.Is(err, providers.ErrUpstreamAuthRejected):
		return apierrors.AiProviderUpstreamAuthRejected, ""
	case errors.Is(err, providers.ErrUpstreamRequestRejected):
		return apierrors.AiProviderUpstreamRequestRejected, ""
	// Nothing was sent: the stored key no longer matches NF_SECRET_KEY,
	// which retrying or checking the network cannot fix.
	case errors.Is(err, providers.ErrKeyDecryptFailed):
		return apierrors.AiProviderKeyDecryptFailed, ""
	// The refusal came from this deployment's destination policy, not
	// from the provider.
	case errors.Is(err, providers.ErrBaseURLDestinationNotAllowed),
		errors.Is(err, providers.ErrInvalidBaseURL):
		return apierrors.AiProviderBaseUrlNotAllowed, ""
	// The provider row cannot be turned into a client at all, so the
	// configuration is what has to change.
	case errors.Is(err, providers.ErrBaseURLNotAllowed),
		errors.Is(err, providers.ErrUnknownKind),
		errors.Is(err, providers.ErrMissingKey):
		return apierrors.AiProviderNotConfigured, ""
	default:
		return apierrors.AiProviderUpstreamUnreachable, ""
	}
}

// PageGenerationFailure is [AIFailure] for page generation, the answer
// REST and MCP both give. The catalog defines a page-specific code for an
// unreachable provider; every other failure keeps its shared code.
func PageGenerationFailure(err error) (*apierr.Spec, string) {
	spec, retryAfter := AIFailure(err)
	if spec == apierrors.AiProviderUpstreamUnreachable {
		return apierrors.PageGenerationUpstreamUnavailable, retryAfter
	}
	return spec, retryAfter
}

// retryAfterFrom returns the Retry-After value carried by a
// [providers.UpstreamError] wrapper, or "" when the error has none.
func retryAfterFrom(err error) string {
	var ue *providers.UpstreamError
	if errors.As(err, &ue) {
		return ue.RetryAfter
	}
	return ""
}
