package ai

import (
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

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
