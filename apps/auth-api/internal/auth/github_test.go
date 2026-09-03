package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFetchGithubPrimaryVerifiedEmail_ReturnsVerifiedPrimary covers
// the happy path: /user/emails reports a primary email flagged as
// verified, and the helper returns it verbatim.
func TestFetchGithubPrimaryVerifiedEmail_ReturnsVerifiedPrimary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/user/emails", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"email":"alt@example.com","primary":false,"verified":true},
			{"email":"primary@example.com","primary":true,"verified":true}
		]`))
	}))
	defer srv.Close()

	c := NewGithubOAuth(GithubOAuthConfig{}).WithAPIBaseURL(srv.URL)
	got, err := c.fetchGithubPrimaryVerifiedEmail(context.Background(), "test-token")
	require.NoError(t, err)
	assert.Equal(t, "primary@example.com", got)
}

// TestFetchGithubPrimaryVerifiedEmail_RejectsUnverifiedPrimary is the
// regression guard for unverified primaries: when the primary email is
// not verified, the helper must surface ErrGithubEmailNotVerified so the
// callback can map it to AUTH.OIDC.EMAIL_NOT_VERIFIED rather than
// silently auto-provisioning an account against an unverified address.
func TestFetchGithubPrimaryVerifiedEmail_RejectsUnverifiedPrimary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"email":"primary@example.com","primary":true,"verified":false},
			{"email":"backup@example.com","primary":false,"verified":true}
		]`))
	}))
	defer srv.Close()

	c := NewGithubOAuth(GithubOAuthConfig{}).WithAPIBaseURL(srv.URL)
	_, err := c.fetchGithubPrimaryVerifiedEmail(context.Background(), "test-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGithubEmailNotVerified),
		"unverified primary email must yield ErrGithubEmailNotVerified, got %v", err)
}

// TestFetchGithubPrimaryVerifiedEmail_RejectsNoPrimary covers the
// degenerate case where the user has only secondary verified emails:
// without a primary entry there is no canonical address to register,
// so the helper must refuse rather than picking an arbitrary one.
func TestFetchGithubPrimaryVerifiedEmail_RejectsNoPrimary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"email":"a@example.com","primary":false,"verified":true},
			{"email":"b@example.com","primary":false,"verified":true}
		]`))
	}))
	defer srv.Close()

	c := NewGithubOAuth(GithubOAuthConfig{}).WithAPIBaseURL(srv.URL)
	_, err := c.fetchGithubPrimaryVerifiedEmail(context.Background(), "test-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGithubEmailNotVerified),
		"missing primary entry must yield ErrGithubEmailNotVerified, got %v", err)
}

// TestFetchGithubPrimaryVerifiedEmail_HTTPErrorBubbles ensures non-2xx
// responses from /user/emails are surfaced as an error rather than
// being mistaken for an empty list (which would also return
// ErrGithubEmailNotVerified, but with a misleading reason). Keeping
// the two cases distinct prevents debugging confusion when GitHub
// returns 401 from a revoked token.
func TestFetchGithubPrimaryVerifiedEmail_HTTPErrorBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewGithubOAuth(GithubOAuthConfig{}).WithAPIBaseURL(srv.URL)
	_, err := c.fetchGithubPrimaryVerifiedEmail(context.Background(), "test-token")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrGithubEmailNotVerified),
		"HTTP failures must not be reported as a verification failure")
}
