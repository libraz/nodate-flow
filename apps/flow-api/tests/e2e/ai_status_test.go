package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// aiStatusBody mirrors GET /workspaces/{wsId}/ai/status.
type aiStatusBody struct {
	Provider struct {
		State string `json:"state"`
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"provider"`
	Embedding struct {
		Status string `json:"status"`
	} `json:"embedding"`
	AutoActions struct {
		Enabled         bool  `json:"enabled"`
		IntervalMinutes int32 `json:"intervalMinutes"`
	} `json:"autoActions"`
}

func aiStatusGet(t *testing.T, ws *helpers.TestTenant, token string) aiStatusBody {
	t.Helper()
	var out aiStatusBody
	doJSON(t, http.MethodGet, amBase(ws)+"/status", token, nil, &out)
	return out
}

// aiStatusClearDefaultModel nulls the provider's default model, the state a
// provider is left in when it was registered without one.
func aiStatusClearDefaultModel(t *testing.T, providerID string) {
	t.Helper()
	_, err := testDB.Exec(`UPDATE ai_providers SET default_model = NULL WHERE public_id = UUID_TO_BIN(?, 0)`, providerID)
	require.NoError(t, err)
	require.False(t, amProviderDefaultModel(t, providerID).Valid)
}

// requireMockReady asserts the mock resolver's status and reports whether it applied.
func requireMockReady(t *testing.T, got aiStatusBody) bool {
	t.Helper()
	if !amMockMode() {
		return false
	}
	require.Equal(t, "ready", got.Provider.State)
	require.Equal(t, "mock", got.Provider.Name)
	return true
}

func TestAIStatusNoProvider(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedInvitedMember(t, owner, "member")
	got := aiStatusGet(t, owner, member.AccessToken)

	if !requireMockReady(t, got) {
		require.Equal(t, "none", got.Provider.State)
		require.Empty(t, got.Provider.Name)
		require.Empty(t, got.Provider.Model)
	}
	require.Contains(t, []string{"enabled", "unconfigured"}, got.Embedding.Status)
	require.True(t, got.AutoActions.Enabled)
	require.Equal(t, int32(5), got.AutoActions.IntervalMinutes)
}

func TestAIStatusNoDefaultModel(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	provID := amOpenAICompat(t, owner, "http://127.0.0.1:1/v1", "some-model")
	aiStatusClearDefaultModel(t, provID)

	got := aiStatusGet(t, owner, owner.AccessToken)
	if requireMockReady(t, got) {
		return
	}
	require.Equal(t, "no_default_model", got.Provider.State)
	require.Equal(t, "Compat some-model", got.Provider.Name)
	require.Empty(t, got.Provider.Model)
}

func TestAIStatusOfficialKindFallback(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	provID := amCreateProvider(t, owner, map[string]any{
		"kind":   "openai",
		"name":   "OpenAI Fixture",
		"apiKey": amFixtureKey,
	})
	aiStatusClearDefaultModel(t, provID)

	got := aiStatusGet(t, owner, owner.AccessToken)
	if requireMockReady(t, got) {
		return
	}
	require.Equal(t, "ready", got.Provider.State)
	require.Equal(t, "OpenAI Fixture", got.Provider.Name)
	require.NotEmpty(t, providers.OfficialDefaultModel(providers.KindOpenAI))
	require.Equal(t, providers.OfficialDefaultModel(providers.KindOpenAI), got.Provider.Model)
}

func TestAIStatusReady(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	amOpenAICompat(t, owner, "http://127.0.0.1:1/v1", "older-model")
	newer := amOpenAICompat(t, owner, "http://127.0.0.1:1/v1", "newer-model")

	got := aiStatusGet(t, owner, owner.AccessToken)
	if requireMockReady(t, got) {
		return
	}
	// The newest enabled provider is the one AI features resolve to.
	require.Equal(t, "ready", got.Provider.State)
	require.Equal(t, "Compat newer-model", got.Provider.Name)
	require.Equal(t, "newer-model", got.Provider.Model)

	// A newest provider without a default model wins over an older one that has one.
	aiStatusClearDefaultModel(t, newer)
	got = aiStatusGet(t, owner, owner.AccessToken)
	require.Equal(t, "no_default_model", got.Provider.State)
	require.Equal(t, "Compat newer-model", got.Provider.Name)
	require.Empty(t, got.Provider.Model)

	// Disabling the newest hands resolution to the older provider.
	_, err := testDB.Exec(`UPDATE ai_providers SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, newer)
	require.NoError(t, err)
	got = aiStatusGet(t, owner, owner.AccessToken)
	require.Equal(t, "ready", got.Provider.State)
	require.Equal(t, "Compat older-model", got.Provider.Name)
	require.Equal(t, "older-model", got.Provider.Model)
}

func TestAIStatusEmbeddingDisabled(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedInvitedMember(t, owner, "member")
	settingsURL := amBase(owner) + "/embedding-settings"

	var inherited struct {
		Status string `json:"status"`
	}
	doJSON(t, http.MethodGet, settingsURL, owner.AccessToken, nil, &inherited)
	require.Equal(t, inherited.Status, aiStatusGet(t, owner, member.AccessToken).Embedding.Status)

	doJSON(t, http.MethodPatch, settingsURL, owner.AccessToken, map[string]string{"mode": "disabled"}, nil)
	require.Equal(t, "disabled", aiStatusGet(t, owner, member.AccessToken).Embedding.Status)
	require.Equal(t, "disabled", aiStatusGet(t, owner, owner.AccessToken).Embedding.Status)

	doJSON(t, http.MethodPatch, settingsURL, owner.AccessToken, map[string]string{"mode": "inherit"}, nil)
	require.Equal(t, inherited.Status, aiStatusGet(t, owner, member.AccessToken).Embedding.Status)
}

func TestAIStatusAutoActions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	got := aiStatusGet(t, owner, owner.AccessToken)
	require.True(t, got.AutoActions.Enabled)
	require.Equal(t, int32(5), got.AutoActions.IntervalMinutes)

	doJSON(t, http.MethodPatch, amBase(owner)+"/auto-action-settings", owner.AccessToken,
		map[string]any{"enabled": false}, nil)
	got = aiStatusGet(t, owner, owner.AccessToken)
	require.False(t, got.AutoActions.Enabled)

	doJSON(t, http.MethodPatch, amBase(owner)+"/auto-action-settings", owner.AccessToken,
		map[string]any{"enabled": true, "intervalMinutes": 15}, nil)
	got = aiStatusGet(t, owner, owner.AccessToken)
	require.True(t, got.AutoActions.Enabled)
	require.Equal(t, int32(15), got.AutoActions.IntervalMinutes)
}

func TestAIStatusGuestReads(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	amOpenAICompat(t, owner, "http://127.0.0.1:1/v1", "guest-visible")
	member := seedInvitedMember(t, owner, "member")
	guest := seedGuestMember(t, owner)

	status, raw := doJSONStatus(t, http.MethodGet, amBase(owner)+"/status", guest.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "guest read body=%s", raw)

	forMember := aiStatusGet(t, owner, member.AccessToken)
	forGuest := aiStatusGet(t, owner, guest.AccessToken)
	require.Equal(t, forMember, forGuest)
	if !requireMockReady(t, forGuest) {
		require.Equal(t, "ready", forGuest.Provider.State)
		require.Equal(t, "guest-visible", forGuest.Provider.Model)
	}
}
