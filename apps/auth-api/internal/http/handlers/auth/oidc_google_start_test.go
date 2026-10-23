package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// TestOIDCGoogleStart_UnconfiguredIsNotConfigured holds an absent Google
// client to the NOT_CONFIGURED answer GitHub and Microsoft give. Reporting
// it as an unreachable provider told the user to retry something that
// cannot start working until an administrator configures it.
func TestOIDCGoogleStart_UnconfiguredIsNotConfigured(t *testing.T) {
	t.Parallel()

	_, err := OIDCGoogleStart(Deps{})(context.Background(), &struct{}{})
	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcGoogleNotConfigured.Code, problem.Type)
	assert.Equal(t, apierrors.AuthOidcGoogleNotConfigured.Status, problem.Status)
}
