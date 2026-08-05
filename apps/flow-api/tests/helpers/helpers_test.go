package helpers

import (
	"net/http"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
	"github.com/stretchr/testify/require"
)

// TestTenantSmoke is the single integration smoke test for the helpers
// package. It boots a real MySQL container, mounts the production
// route set, registers a tenant, calls /me with the access token, and
// asserts the returned profile matches the registered values.
//
// The test is skipped under `go test -short` and unless
// NF_TEST_INTEGRATION is set, so unit-only runs (and machines without
// Docker) stay fast.
func TestTenantSmoke(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	t.Parallel()

	mysqlInst := StartShared(t)
	srv := StartTestServer(t, mysqlInst.DB)

	tenant := CreateTestTenant(t, srv.BaseURL)
	t.Cleanup(func() { CleanupTenant(t, tenant) })

	require.NotEmpty(t, tenant.AccessToken)
	require.NotEmpty(t, tenant.UserPublicID)

	var me struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Locale      string `json:"locale"`
	}
	doJSON(t, http.MethodGet, srv.BaseURL+"/me", tenant.AccessToken, nil, &me)

	require.Equal(t, tenant.UserPublicID, me.ID, "/me id should match registered user")
	require.Equal(t, tenant.Email, me.Email)
	require.Equal(t, tenant.DisplayName, me.DisplayName)
	require.Equal(t, "en", me.Locale)
}
