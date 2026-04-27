package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// testCipherKey is a fixed 32-byte key for test Cipher construction.
var testCipherKey = []byte("test-cipher-key-aaaaaaaaaaaaaaaa")

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testCipherKey)
	require.NoError(t, err)
	return c
}

// --- List handler ---

func TestList_ReturnsAllThreeProviders(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{}
	deps := Deps{Queries: q, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "list", http.MethodGet, "/me/integrations", 42, "")

	require.Equal(t, http.StatusOK, resp.Code)
	var out struct {
		Providers []ProviderStatus `json:"providers"`
	}
	decodeBody(t, resp, &out)
	require.Len(t, out.Providers, 3, "must list exactly 3 providers regardless of configuration")

	names := make([]string, len(out.Providers))
	for i, p := range out.Providers {
		names[i] = p.Provider
	}
	assert.Equal(t, []string{"github", "slack", "google_calendar"}, names,
		"providers must be in deterministic order for stable UI rendering")
}

func TestList_MarksConfiguredProviders(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	q := &fakeQueries{}
	deps := Deps{Queries: q, Registry: reg}
	resp := serve(t, deps, "list", http.MethodGet, "/me/integrations", 1, "")

	var out struct {
		Providers []ProviderStatus `json:"providers"`
	}
	decodeBody(t, resp, &out)
	for _, p := range out.Providers {
		if p.Provider == "github" {
			assert.True(t, p.Configured, "github must be marked configured when Registry has it")
		} else {
			assert.False(t, p.Configured, "%s must be unconfigured", p.Provider)
		}
	}
}

func TestList_IncludesExistingConnection(t *testing.T) {
	t.Parallel()
	pub := types.New()
	q := &fakeQueries{
		listRows: []generated.ListUserIntegrationsRow{
			{
				PublicID:             pub,
				Provider:             "github",
				ExternalAccountID:    "12345",
				ExternalAccountLabel: "octocat@github.com",
				Scopes:               "read:user repo",
				ConnectedAt:          time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	deps := Deps{Queries: q, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "list", http.MethodGet, "/me/integrations", 1, "")

	var out struct {
		Providers []ProviderStatus `json:"providers"`
	}
	decodeBody(t, resp, &out)
	var ghStatus *ProviderStatus
	for i := range out.Providers {
		if out.Providers[i].Provider == "github" {
			ghStatus = &out.Providers[i]
			break
		}
	}
	require.NotNil(t, ghStatus)
	require.NotNil(t, ghStatus.Connection, "existing connection must be attached")
	assert.Equal(t, pub.String(), ghStatus.Connection.ID)
	assert.Equal(t, "12345", ghStatus.Connection.ExternalAccountID)
	assert.Equal(t, "octocat@github.com", ghStatus.Connection.ExternalAccountLabel)
}

func TestList_RequiresAuth(t *testing.T) {
	t.Parallel()
	deps := Deps{Queries: &fakeQueries{}, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "list", http.MethodGet, "/me/integrations", 0, "") // no actor
	assert.GreaterOrEqual(t, resp.Code, 400,
		"unauthenticated requests must be rejected")
}

// --- Connect handler ---

func TestConnect_UnsupportedProvider(t *testing.T) {
	t.Parallel()
	deps := Deps{Queries: &fakeQueries{}, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "connect", http.MethodPost, "/me/integrations/bitbucket/connect", 1,
		`{"redirectTo":""}`)
	// Huma returns 422 for failed enum validation.
	assert.True(t, resp.Code == http.StatusBadRequest || resp.Code == http.StatusUnprocessableEntity,
		"unsupported provider name must be rejected, got %d", resp.Code)
}

func TestConnect_UnconfiguredProvider(t *testing.T) {
	t.Parallel()
	// Registry empty — no providers configured.
	deps := Deps{Queries: &fakeQueries{}, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "connect", http.MethodPost, "/me/integrations/github/connect", 1,
		`{"redirectTo":""}`)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code,
		"unconfigured provider must return 503")
}

func TestConnect_ConfiguredProvider_ReturnsAuthorizeURL(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	q := &fakeQueries{}
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		PublicBaseURL: "https://auth.example.com",
	}
	resp := serve(t, deps, "connect", http.MethodPost, "/me/integrations/github/connect", 1,
		`{}`)
	require.Equal(t, http.StatusOK, resp.Code, "body=%s", resp.Body.String())
	var out struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	decodeBody(t, resp, &out)
	assert.NotEmpty(t, out.AuthorizeURL, "must return an authorize URL")
	assert.True(t, q.oauthStateCreated, "must create an OAuth state row for CSRF protection")
}

func TestConnect_NoCipher_Returns503(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	deps := Deps{Queries: &fakeQueries{}, Registry: reg, Cipher: nil}
	resp := serve(t, deps, "connect", http.MethodPost, "/me/integrations/github/connect", 1,
		`{}`)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code,
		"missing cipher means encryption unavailable → 503")
}

// --- Disconnect handler ---

func TestDisconnect_NotFound(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{findByPubErr: sql.ErrNoRows}
	deps := Deps{Queries: q, Registry: integrationspkg.NewRegistry()}
	fakeUUID := "01961234-5678-7000-8000-000000000000"
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/"+fakeUUID, 1, "")
	assert.Equal(t, http.StatusNotFound, resp.Code,
		"disconnect non-existent connection must return 404")
}

func TestDisconnect_InvalidUUID(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{}
	deps := Deps{Queries: q, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/not-a-uuid", 1, "")
	assert.GreaterOrEqual(t, resp.Code, 400,
		"invalid UUID must be rejected")
}

func TestDisconnect_HappyPath(t *testing.T) {
	t.Parallel()
	pub := types.New()
	q := &fakeQueries{
		findByPubRow: generated.FindUserIntegrationByPublicIdRow{
			ID:       7,
			PublicID: pub,
			Provider: "github",
		},
	}
	deps := Deps{Queries: q, Registry: integrationspkg.NewRegistry()}
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/"+pub.String(), 1, "")
	require.Equal(t, http.StatusOK, resp.Code, "body=%s", resp.Body.String())
	assert.True(t, q.deleted, "must delete the integration row")
	var out struct {
		Ok bool `json:"ok"`
	}
	decodeBody(t, resp, &out)
	assert.True(t, out.Ok)
}

func TestDisconnect_RevokeFailure_DoesNotBlockDeletion(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	pub := types.New()
	accessCT, _ := c.Encrypt([]byte("access-tok"))

	// Create a provider that always fails Revoke.
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubRevokeProvider{
				stubProvider: stubProvider{name: "github"},
				revokeErr:    errors.New("provider down"),
			}, nil
		},
	)
	q := &fakeQueries{
		findByPubRow: generated.FindUserIntegrationByPublicIdRow{
			ID:       10,
			PublicID: pub,
			Provider: "github",
		},
		findByProvRow: generated.FindUserIntegrationByUserProviderRow{
			ID:                    10,
			PublicID:              pub,
			AccessTokenCiphertext: accessCT,
		},
	}
	deps := Deps{Queries: q, Registry: reg, Cipher: c}
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/"+pub.String(), 1, "")
	require.Equal(t, http.StatusOK, resp.Code,
		"disconnect must succeed even when provider revoke fails — best-effort")
	assert.True(t, q.deleted,
		"local row must be deleted regardless of revoke outcome")
}

func TestDisconnect_NoCipher_StillDeletes(t *testing.T) {
	t.Parallel()
	pub := types.New()
	q := &fakeQueries{
		findByPubRow: generated.FindUserIntegrationByPublicIdRow{
			ID:       5,
			PublicID: pub,
			Provider: "github",
		},
	}
	// Registry has the provider, but Cipher is nil → revoke skipped.
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	deps := Deps{Queries: q, Registry: reg, Cipher: nil}
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/"+pub.String(), 1, "")
	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, q.deleted,
		"when cipher is unavailable, revoke is skipped but deletion proceeds")
}

// --- Callback handler ---

func TestCallback_MissingCode_RedirectsWithError(t *testing.T) {
	t.Parallel()
	deps := Deps{
		Queries:    &fakeQueries{},
		Registry:   integrationspkg.NewRegistry(),
		WebBaseURL: "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?state=abc&error=access_denied")
	assert.Equal(t, http.StatusFound, resp.Code,
		"callback with error must 302 redirect")
	loc := resp.Header().Get("Location")
	assert.Contains(t, loc, "integration_error=oauth_denied",
		"redirect must include the error reason")
	assert.True(t, strings.HasPrefix(loc, "https://app.example.com/"),
		"redirect target must be the web app")
}

func TestCallback_InvalidState_RedirectsWithError(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	q := &fakeQueries{consumeStateErr: sql.ErrNoRows}
	deps := Deps{
		Queries:    q,
		Registry:   reg,
		Cipher:     newTestCipher(t),
		WebBaseURL: "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=xxx&state=bogus")
	assert.Equal(t, http.StatusFound, resp.Code)
	loc := resp.Header().Get("Location")
	assert.Contains(t, loc, "integration_error=state_invalid",
		"unknown state must redirect with state_invalid error")
}

func TestCallback_ExpiredState_RedirectsWithError(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
	)
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "github",
			ExpiresAt: time.Now().Add(-time.Hour), // expired
		},
	}
	deps := Deps{
		Queries:    q,
		Registry:   reg,
		Cipher:     newTestCipher(t),
		WebBaseURL: "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=xxx&state=valid")
	assert.Equal(t, http.StatusFound, resp.Code)
	loc := resp.Header().Get("Location")
	assert.Contains(t, loc, "integration_error=state_expired")
}

func TestCallback_ProviderMismatch_RedirectsWithError(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "github"}, nil
		},
		func() (integrationspkg.Provider, error) {
			return &stubProvider{name: "slack"}, nil
		},
	)
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "slack", // mismatch with URL path "github"
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	deps := Deps{
		Queries:    q,
		Registry:   reg,
		Cipher:     newTestCipher(t),
		WebBaseURL: "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=xxx&state=valid")
	assert.Equal(t, http.StatusFound, resp.Code)
	loc := resp.Header().Get("Location")
	assert.Contains(t, loc, "integration_error=state_provider_mismatch",
		"state provider and URL provider must match to prevent CSRF replay")
}

func TestCallback_HappyPath_RedirectsToSettings(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens: &integrationspkg.TokenSet{
					AccessToken: "gho_test",
					Scopes:      []string{"read:user"},
				},
				account: &integrationspkg.Account{
					ExternalID: "42",
					Label:      "octocat",
				},
			}, nil
		},
	)
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "github",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=authcode&state=valid")
	require.Equal(t, http.StatusFound, resp.Code, "body=%s", resp.Body.String())
	loc := resp.Header().Get("Location")
	assert.Contains(t, loc, "app.example.com/settings/integrations",
		"successful callback must redirect to the integrations settings page")
	assert.Contains(t, loc, "connected=github",
		"redirect must include the connected provider for success toast")
	assert.True(t, q.upserted, "must upsert the user_integrations row")
}

func TestCallback_HappyPath_CustomRedirect(t *testing.T) {
	t.Parallel()
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens:       &integrationspkg.TokenSet{AccessToken: "t"},
				account:      &integrationspkg.Account{ExternalID: "1", Label: "x"},
			}, nil
		},
	)
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:     1,
			Provider:   "github",
			ExpiresAt:  time.Now().Add(10 * time.Minute),
			RedirectTo: sql.NullString{String: "https://custom.app/done", Valid: true},
		},
	}
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	resp := serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=c&state=s")
	loc := resp.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://custom.app/done"),
		"must honour client-supplied redirectTo from the state row")
}

func TestCallback_ExchangeTokensAreEncrypted(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens: &integrationspkg.TokenSet{
					AccessToken:  "secret-access-token",
					RefreshToken: "secret-refresh-token",
					Scopes:       []string{"read:user"},
				},
				account: &integrationspkg.Account{ExternalID: "1", Label: "x"},
			}, nil
		},
	)
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "github",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        c,
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	_ = serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=c&state=s")

	require.True(t, q.upserted, "must upsert")
	// Access token must be encrypted, not plaintext.
	assert.NotEqual(t, "secret-access-token", string(q.upsertParams.AccessTokenCiphertext),
		"access token must be stored encrypted, never as plaintext")
	decrypted, err := c.Decrypt(q.upsertParams.AccessTokenCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "secret-access-token", string(decrypted),
		"decrypting stored ciphertext must yield the original token")

	// Refresh token.
	assert.True(t, q.upsertParams.RefreshTokenCiphertext.Valid)
	decryptedRefresh, err := c.Decrypt([]byte(q.upsertParams.RefreshTokenCiphertext.String))
	require.NoError(t, err)
	assert.Equal(t, "secret-refresh-token", string(decryptedRefresh))
}

// TestCallback_DeletesStateBeforeExchange asserts that the OAuth state
// row is removed from the database before the (potentially expensive)
// provider Exchange call runs. If a network blip caused Exchange to
// retry, the state row must already be gone so the same code cannot be
// replayed.
func TestCallback_DeletesStateBeforeExchange(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "github",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens:       &integrationspkg.TokenSet{AccessToken: "t"},
				account:      &integrationspkg.Account{ExternalID: "1", Label: "x"},
				onCall:       func() { q.log("Exchange") },
			}, nil
		},
	)
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	_ = serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=c&state=valid-state")

	require.Contains(t, q.callLog, "DeleteOauthState",
		"DeleteOauthState must be called for the consumed state")
	require.Contains(t, q.callLog, "Exchange",
		"provider Exchange must be called on the happy path")

	deleteIdx := -1
	exchIdx := -1
	for i, ev := range q.callLog {
		if ev == "DeleteOauthState" && deleteIdx == -1 {
			deleteIdx = i
		}
		if ev == "Exchange" && exchIdx == -1 {
			exchIdx = i
		}
	}
	assert.Less(t, deleteIdx, exchIdx,
		"DeleteOauthState must run BEFORE provider Exchange to close the re-use window")
	assert.Contains(t, q.deletedStates, "valid-state",
		"the consumed state value must be the one deleted")
}

// TestCallback_HappyPath_RecordsLinkedAudit asserts that a successful
// callback emits an integration.linked audit entry with the provider in
// metadata, matching the link side-effect contract.
func TestCallback_HappyPath_RecordsLinkedAudit(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    77,
			Provider:  "github",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				tokens:       &integrationspkg.TokenSet{AccessToken: "t"},
				account:      &integrationspkg.Account{ExternalID: "1", Label: "x"},
			}, nil
		},
	)
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		Audit:         sink,
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	_ = serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=c&state=s")

	entries := sink.snapshot()
	require.Len(t, entries, 1, "exactly one audit entry must be recorded for a successful link")
	got := entries[0]
	assert.Equal(t, "integration.linked", got.Action)
	assert.Equal(t, uint32(77), got.ActorID, "actor must be the user from the consumed state row")
	assert.Equal(t, "user_integration", got.ResourceType)
	assert.Equal(t, "github", got.Metadata["provider"])
}

// TestCallback_ExchangeFailure_NoLinkedAudit asserts the audit entry is
// NOT emitted when the OAuth Exchange step fails — the integration was
// never linked, so the audit log must not pretend it was.
func TestCallback_ExchangeFailure_NoLinkedAudit(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	q := &fakeQueries{
		consumeStateRow: generated.ConsumeOauthStateRow{
			UserID:    1,
			Provider:  "github",
			ExpiresAt: time.Now().Add(10 * time.Minute),
		},
	}
	reg := integrationspkg.NewRegistry(
		func() (integrationspkg.Provider, error) {
			return &stubExchangeProvider{
				stubProvider: stubProvider{name: "github"},
				exchErr:      errors.New("exchange boom"),
			}, nil
		},
	)
	deps := Deps{
		Queries:       q,
		Registry:      reg,
		Cipher:        newTestCipher(t),
		Audit:         sink,
		PublicBaseURL: "https://auth.example.com",
		WebBaseURL:    "https://app.example.com",
	}
	_ = serveNoAuth(t, deps, "callback", http.MethodGet,
		"/oauth/callback/github?code=c&state=s")

	assert.Empty(t, sink.snapshot(),
		"failed exchange must not emit a linked audit entry")
}

// TestDisconnect_RecordsUnlinkedAudit asserts that a successful
// disconnect emits the integration.unlinked audit entry.
func TestDisconnect_RecordsUnlinkedAudit(t *testing.T) {
	t.Parallel()
	pub := types.New()
	q := &fakeQueries{
		findByPubRow: generated.FindUserIntegrationByPublicIdRow{
			ID:       7,
			PublicID: pub,
			Provider: "slack",
		},
	}
	sink := &captureSink{}
	deps := Deps{
		Queries:  q,
		Registry: integrationspkg.NewRegistry(),
		Audit:    sink,
	}
	resp := serve(t, deps, "disconnect", http.MethodDelete, "/me/integrations/"+pub.String(), 99, "")
	require.Equal(t, http.StatusOK, resp.Code, "body=%s", resp.Body.String())

	entries := sink.snapshot()
	require.Len(t, entries, 1, "disconnect must emit one audit entry")
	got := entries[0]
	assert.Equal(t, "integration.unlinked", got.Action)
	assert.Equal(t, uint32(99), got.ActorID, "actor must be the requester, not the row owner")
	assert.Equal(t, "user_integration", got.ResourceType)
	assert.Equal(t, "slack", got.Metadata["provider"])
}

// --- ConnectionSummary mapping ---

func TestRowToConnectionSummary_MapsAllFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	expires := time.Date(2025, 6, 15, 13, 0, 0, 0, time.UTC)
	refreshed := time.Date(2025, 6, 15, 11, 30, 0, 0, time.UTC)
	pub := types.New()

	row := generated.ListUserIntegrationsRow{
		PublicID:             pub,
		Provider:             "google_calendar",
		ExternalAccountID:    "112233",
		ExternalAccountLabel: "user@gmail.com",
		Scopes:               "openid email calendar.readonly",
		ConnectedAt:          now,
		LastRefreshedAt:      sql.NullTime{Time: refreshed, Valid: true},
		AccessTokenExpiresAt: sql.NullTime{Time: expires, Valid: true},
	}
	c := rowToConnectionSummary(row)

	assert.Equal(t, pub.String(), c.ID)
	assert.Equal(t, "google_calendar", c.Provider)
	assert.Equal(t, "112233", c.ExternalAccountID)
	assert.Equal(t, "user@gmail.com", c.ExternalAccountLabel)
	assert.Equal(t, "openid email calendar.readonly", c.Scopes)
	assert.Equal(t, now.Unix(), c.ConnectedAt)
	require.NotNil(t, c.LastRefreshedAt)
	assert.Equal(t, refreshed.Unix(), *c.LastRefreshedAt)
	require.NotNil(t, c.AccessTokenExpiresAt)
	assert.Equal(t, expires.Unix(), *c.AccessTokenExpiresAt)
}

func TestRowToConnectionSummary_NullableFieldsOmitted(t *testing.T) {
	t.Parallel()
	row := generated.ListUserIntegrationsRow{
		PublicID:             types.New(),
		Provider:             "github",
		ExternalAccountID:    "42",
		ExternalAccountLabel: "@octocat",
		ConnectedAt:          time.Now(),
		// LastRefreshedAt and AccessTokenExpiresAt are zero-valued (null).
	}
	c := rowToConnectionSummary(row)
	assert.Nil(t, c.LastRefreshedAt, "null last_refreshed_at must be nil, not zero")
	assert.Nil(t, c.AccessTokenExpiresAt, "null access_token_expires_at must be nil, not zero")
}

// --- helper: isSupportedProvider ---

func TestIsSupportedProvider(t *testing.T) {
	t.Parallel()
	assert.True(t, isSupportedProvider("github"))
	assert.True(t, isSupportedProvider("slack"))
	assert.True(t, isSupportedProvider("google_calendar"))
	assert.False(t, isSupportedProvider("bitbucket"))
	assert.False(t, isSupportedProvider(""))
}

// --- helper: callbackURL ---

func TestCallbackURL(t *testing.T) {
	t.Parallel()
	deps := Deps{PublicBaseURL: "https://auth.example.com"}
	assert.Equal(t, "https://auth.example.com/oauth/callback/github",
		callbackURL(deps, "github"))

	deps.PublicBaseURL = "https://auth.example.com/"
	assert.Equal(t, "https://auth.example.com/oauth/callback/slack",
		callbackURL(deps, "slack"),
		"trailing slash must be normalised")
}

// --- test infrastructure ---

// serve builds a minimal Huma API with the specified handler, injects
// the actor into the context, and returns the response recorder.
func serve(t *testing.T, deps Deps, op, method, path string, actorID uint32, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	if actorID > 0 {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := authn.WithActor(req.Context(), actorID)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
	}
	api := humachi.New(r, huma.DefaultConfig("test", "0.0.0"))
	registerHandler(api, deps, op)

	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// serveNoAuth is like serve but without injecting an actor (for
// unauthenticated endpoints like the OAuth callback).
func serveNoAuth(t *testing.T, deps Deps, op, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, deps, op, method, path, 0, "")
}

func registerHandler(api huma.API, deps Deps, op string) {
	switch op {
	case "list":
		huma.Register(api, huma.Operation{
			OperationID: "test-list",
			Method:      http.MethodGet,
			Path:        "/me/integrations",
		}, List(deps))
	case "connect":
		huma.Register(api, huma.Operation{
			OperationID: "test-connect",
			Method:      http.MethodPost,
			Path:        "/me/integrations/{provider}/connect",
		}, Connect(deps))
	case "disconnect":
		huma.Register(api, huma.Operation{
			OperationID: "test-disconnect",
			Method:      http.MethodDelete,
			Path:        "/me/integrations/{id}",
		}, Disconnect(deps))
	case "callback":
		huma.Register(api, huma.Operation{
			OperationID: "test-callback",
			Method:      http.MethodGet,
			Path:        "/oauth/callback/{provider}",
		}, Callback(deps))
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out),
		"decode response body: %s", rec.Body.String())
}

// --- test doubles ---

type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) AuthURL(state, redirectURI string) string {
	return "https://example.com/auth?state=" + state + "&redirect_uri=" + redirectURI
}
func (s *stubProvider) Exchange(_ context.Context, _, _ string) (*integrationspkg.TokenSet, *integrationspkg.Account, error) {
	return &integrationspkg.TokenSet{AccessToken: "tok"}, &integrationspkg.Account{ExternalID: "1", Label: "test"}, nil
}
func (s *stubProvider) Refresh(_ context.Context, _ string) (*integrationspkg.TokenSet, error) {
	return nil, integrationspkg.ErrRefreshNotSupported
}
func (s *stubProvider) Revoke(_ context.Context, _ integrationspkg.TokenSet) error {
	return nil
}

type stubRevokeProvider struct {
	stubProvider
	revokeErr error
}

func (s *stubRevokeProvider) Revoke(_ context.Context, _ integrationspkg.TokenSet) error {
	return s.revokeErr
}

type stubExchangeProvider struct {
	stubProvider
	tokens  *integrationspkg.TokenSet
	account *integrationspkg.Account
	onCall  func()
	exchErr error
}

func (s *stubExchangeProvider) Exchange(_ context.Context, _, _ string) (*integrationspkg.TokenSet, *integrationspkg.Account, error) {
	if s.onCall != nil {
		s.onCall()
	}
	if s.exchErr != nil {
		return nil, nil, s.exchErr
	}
	return s.tokens, s.account, nil
}

// captureSink is an audit.Sink that records every entry for assertion.
type captureSink struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (c *captureSink) Record(_ context.Context, e audit.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *captureSink) snapshot() []audit.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// fakeQueries implements HandlerQuerier for unit tests.
type fakeQueries struct {
	mu sync.Mutex
	// callLog records the order in which fake methods are invoked so
	// tests can assert ordering invariants such as
	// DeleteOauthState-before-Exchange.
	callLog []string

	// ListUserIntegrations
	listRows []generated.ListUserIntegrationsRow

	// CreateOauthState
	oauthStateCreated bool

	// ConsumeOauthState
	consumeStateRow generated.ConsumeOauthStateRow
	consumeStateErr error

	// DeleteOauthState
	deletedStates []string

	// UpsertUserIntegration
	upserted     bool
	upsertParams generated.UpsertUserIntegrationParams

	// FindUserIntegrationByPublicId
	findByPubRow generated.FindUserIntegrationByPublicIdRow
	findByPubErr error

	// FindUserIntegrationByUserProvider (for revoke)
	findByProvRow generated.FindUserIntegrationByUserProviderRow
	findByProvErr error

	// DeleteUserIntegration
	deleted bool
}

// log appends an event to the call order trace. Safe for concurrent
// use because Huma may invoke handlers from a request goroutine.
func (f *fakeQueries) log(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callLog = append(f.callLog, event)
}

func (f *fakeQueries) ListUserIntegrations(_ context.Context, _ uint32) ([]generated.ListUserIntegrationsRow, error) {
	return f.listRows, nil
}

func (f *fakeQueries) CreateOauthState(_ context.Context, _ generated.CreateOauthStateParams) error {
	f.oauthStateCreated = true
	return nil
}

func (f *fakeQueries) ConsumeOauthState(_ context.Context, _ string) (generated.ConsumeOauthStateRow, error) {
	f.log("ConsumeOauthState")
	if f.consumeStateErr != nil {
		return generated.ConsumeOauthStateRow{}, f.consumeStateErr
	}
	return f.consumeStateRow, nil
}

func (f *fakeQueries) DeleteOauthState(_ context.Context, state string) error {
	f.log("DeleteOauthState")
	f.mu.Lock()
	f.deletedStates = append(f.deletedStates, state)
	f.mu.Unlock()
	return nil
}

func (f *fakeQueries) PurgeExpiredOauthStates(_ context.Context) error {
	return nil
}

func (f *fakeQueries) UpsertUserIntegration(_ context.Context, arg generated.UpsertUserIntegrationParams) (int64, error) {
	f.upserted = true
	f.upsertParams = arg
	return 1, nil
}

// Method name mirrors the sqlc-generated interface; it cannot be renamed without
// renaming the SQL query and regenerating across both apps.
//
//nolint:revive // see comment above
func (f *fakeQueries) FindUserIntegrationByPublicId(ctx context.Context, arg generated.FindUserIntegrationByPublicIdParams) (generated.FindUserIntegrationByPublicIdRow, error) {
	if f.findByPubErr != nil {
		return generated.FindUserIntegrationByPublicIdRow{}, f.findByPubErr
	}
	return f.findByPubRow, nil
}

func (f *fakeQueries) FindUserIntegrationByUserProvider(_ context.Context, _ generated.FindUserIntegrationByUserProviderParams) (generated.FindUserIntegrationByUserProviderRow, error) {
	if f.findByProvErr != nil {
		return generated.FindUserIntegrationByUserProviderRow{}, f.findByProvErr
	}
	return f.findByProvRow, nil
}

func (f *fakeQueries) DeleteUserIntegration(_ context.Context, _ generated.DeleteUserIntegrationParams) error {
	f.deleted = true
	return nil
}
