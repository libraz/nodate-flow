package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// TestUnconfiguredOIDCProvidersAreDisabledOverTheWire builds the router
// with no OIDC client set. Typed nil pointers must not turn into non-nil
// handler interfaces: capabilities reports every provider off and the
// GitHub and Microsoft start endpoints refuse with their NOT_CONFIGURED
// code rather than a recovered panic.
func TestUnconfiguredOIDCProvidersAreDisabledOverTheWire(t *testing.T) {
	t.Parallel()
	h := Build(stubDeps(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/capabilities", nil))
	require.Equalf(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var caps struct {
		OIDCGoogle    bool `json:"oidcGoogle"`
		OIDCGithub    bool `json:"oidcGithub"`
		OIDCMicrosoft bool `json:"oidcMicrosoft"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &caps))
	assert.False(t, caps.OIDCGoogle)
	assert.False(t, caps.OIDCGithub)
	assert.False(t, caps.OIDCMicrosoft)

	for path, want := range map[string]*apierrors.Spec{
		"/auth/oidc/github/start":    apierrors.AuthOidcGithubNotConfigured,
		"/auth/oidc/microsoft/start": apierrors.AuthOidcMicrosoftNotConfigured,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equalf(t, want.Status, rec.Code, "%s body=%s", path, rec.Body.String())
		assert.Containsf(t, rec.Body.String(), want.Code, "%s", path)
	}
}
