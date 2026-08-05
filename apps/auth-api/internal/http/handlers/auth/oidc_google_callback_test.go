package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// TestOIDCGoogleCallback_SurfacesProviderRejection asserts that an IdP
// callback carrying `?error=...&error_description=...` is surfaced as
// AUTH.OIDC.PROVIDER_REJECTED with HTTP 400 BEFORE the handler even
// touches the OIDC client. Without this guard a Google-side rejection
// (denied consent, rejected scopes, misconfigured app) would surface
// as a generic AUTH.OIDC.ID_TOKEN_INVALID and obscure the real cause.
//
// The test deliberately leaves deps.OIDC nil to prove the
// provider-rejection branch fires before the configured-provider
// check; a regression that re-orders the two would surface here as
// either a panic or AUTH.OIDC.PROVIDER_UNREACHABLE.
func TestOIDCGoogleCallback_SurfacesProviderRejection(t *testing.T) {
	t.Parallel()

	deps := Deps{
		Audit: audit.NoopSink{},
	}
	handler := OIDCGoogleCallback(deps)
	_, err := handler(context.Background(), &OIDCCallbackInput{
		Error:            "access_denied",
		ErrorDescription: "user denied",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Code, problem.Type,
		"provider-rejected callback must surface AUTH.OIDC.PROVIDER_REJECTED")
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Status, problem.Status)
	assert.Equal(t, "google", problem.Extensions["provider"])
	assert.Equal(t, "access_denied", problem.Extensions["provider_error"])
	assert.Equal(t, "user denied", problem.Extensions["provider_error_description"])
}

// TestOIDCGoogleCallback_OmitsEmptyErrorDescription asserts the
// handler does not insert a `provider_error_description` extension
// member when the IdP omits the optional human-readable description.
// This keeps the wire envelope minimal on the common path while
// still emitting the slug that drives downstream diagnostics.
func TestOIDCGoogleCallback_OmitsEmptyErrorDescription(t *testing.T) {
	t.Parallel()

	deps := Deps{
		Audit: audit.NoopSink{},
	}
	handler := OIDCGoogleCallback(deps)
	_, err := handler(context.Background(), &OIDCCallbackInput{
		Error: "access_denied",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	_, hasDesc := problem.Extensions["provider_error_description"]
	assert.False(t, hasDesc,
		"empty error_description must be omitted from extensions")
	assert.Equal(t, "access_denied", problem.Extensions["provider_error"])
}
