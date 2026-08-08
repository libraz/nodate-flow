package ai

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// problemOf digs the canonical problem envelope out of a handler error.
func problemOf(t *testing.T, err error) *handlerutil.ProblemDetails {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var pd *handlerutil.ProblemDetails
	if !errors.As(err, &pd) {
		t.Fatalf("error %v is not a problem envelope", err)
	}
	return pd
}

// TestRefusedEndpointDoesNotReadAsANetworkFault is the point of the
// dedicated code. A base URL this deployment refuses to contact is a
// configuration decision made here, and reporting it as an unreachable
// provider sends the reader to the network and the vendor's status page —
// neither of which will ever explain it, and neither of which mentions the
// setting that permits the address.
func TestRefusedEndpointDoesNotReadAsANetworkFault(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"refused destination", fmt.Errorf("complete: %w", providers.ErrBaseURLDestinationNotAllowed)},
		{"unusable url", fmt.Errorf("complete: %w", providers.ErrInvalidBaseURL)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := problemOf(t, mapProviderError(tc.err)).Type
			if got == apierrors.AiProviderUpstreamUnreachable.Code {
				t.Fatal("a refused endpoint must not be reported as an unreachable upstream")
			}
			if got != apierrors.AiProviderBaseUrlNotAllowed.Code {
				t.Fatalf("code = %s, want %s", got, apierrors.AiProviderBaseUrlNotAllowed.Code)
			}
		})
	}
}

// TestGenuineTransportFailureStaysUnreachable guards the other direction:
// the new code is for the destination policy only, and a provider that
// really could not be reached must keep saying so.
func TestGenuineTransportFailureStaysUnreachable(t *testing.T) {
	t.Parallel()

	got := problemOf(t, mapProviderError(fmt.Errorf("complete: %w", providers.ErrUpstreamUnreachable))).Type
	if got != apierrors.AiProviderUpstreamUnreachable.Code {
		t.Fatalf("code = %s, want %s", got, apierrors.AiProviderUpstreamUnreachable.Code)
	}
}

// TestCreateProviderErrorNamesTheEndpointPolicy covers the submit-time
// path. An admin told only that a body field is invalid has nowhere to go:
// the address is well-formed and the reason it was refused is a policy
// they cannot see from the form.
func TestCreateProviderErrorNamesTheEndpointPolicy(t *testing.T) {
	t.Parallel()

	got := problemOf(t, createProviderError(fmt.Errorf("validate: %w", providers.ErrBaseURLDestinationNotAllowed))).Type
	if got != apierrors.AiProviderBaseUrlNotAllowed.Code {
		t.Fatalf("code = %s, want %s", got, apierrors.AiProviderBaseUrlNotAllowed.Code)
	}

	// Everything else about the submitted body keeps the generic answer.
	got = problemOf(t, createProviderError(fmt.Errorf("validate: %w", providers.ErrMissingKey))).Type
	if got != apierrors.ValidationBodyFieldInvalid.Code {
		t.Fatalf("code = %s, want %s", got, apierrors.ValidationBodyFieldInvalid.Code)
	}
}

// TestBaseUrlErrorTellsTheOperatorHowToAllowIt keeps the escape hatch
// discoverable from the response itself. A local model server is a
// supported deployment, so the refusal has to carry the name of the
// setting that permits it rather than leaving the operator to find it in
// the source.
func TestBaseUrlErrorTellsTheOperatorHowToAllowIt(t *testing.T) {
	t.Parallel()

	pd := problemOf(t, mapProviderError(providers.ErrBaseURLDestinationNotAllowed))
	if pd.Status != 412 {
		t.Errorf("status = %d, want 412 (the configuration is what has to change)", pd.Status)
	}
	if !strings.Contains(pd.UserAction, "NF_FLOW_AI_ALLOW_PRIVATE") {
		t.Errorf("userAction %q does not name the setting that permits a local endpoint", pd.UserAction)
	}
}
