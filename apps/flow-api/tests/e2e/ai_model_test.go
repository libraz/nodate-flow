package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// amProviderModel mirrors the ProviderModel response body.
type amProviderModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Enabled     bool   `json:"enabled"`
	IsDefault   bool   `json:"isDefault"`
	AgentCount  int    `json:"agentCount"`
}

const amFixtureKey = "sk-test-model-mgmt-fixture-0123456789" //#nosec G101 -- synthetic test fixture, never a real key

// amMockMode reports whether the suite runs with the mock LLM wired in.
func amMockMode() bool {
	switch os.Getenv("NF_FLOW_AI_MOCK") {
	case "", "0", "false":
		return false
	}
	return true
}

func amBase(tt *helpers.TestTenant) string {
	return testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai"
}

func amModelsURL(tt *helpers.TestTenant, providerID string) string {
	return amBase(tt) + "/providers/" + providerID + "/models"
}

// amCreateProvider registers a provider and returns its public id.
func amCreateProvider(t *testing.T, tt *helpers.TestTenant, body map[string]any) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, amBase(tt)+"/providers", tt.AccessToken, body, &out)
	require.NotEmpty(t, out.ID)
	return out.ID
}

// amAnthropic registers an anthropic provider (one auto-registered model).
func amAnthropic(t *testing.T, tt *helpers.TestTenant) string {
	t.Helper()
	return amCreateProvider(t, tt, map[string]any{
		"kind":   "anthropic",
		"name":   "Anthropic Fixture",
		"apiKey": amFixtureKey,
	})
}

// amOpenAICompat registers an openai_compat provider with one model.
func amOpenAICompat(t *testing.T, tt *helpers.TestTenant, baseURL, defaultModel string) string {
	t.Helper()
	return amCreateProvider(t, tt, map[string]any{
		"kind":         "openai_compat",
		"name":         "Compat " + defaultModel,
		"baseUrl":      baseURL,
		"defaultModel": defaultModel,
		"apiKey":       amFixtureKey,
	})
}

func amList(t *testing.T, tt *helpers.TestTenant, providerID string) []amProviderModel {
	t.Helper()
	var out struct {
		Models []amProviderModel `json:"models"`
	}
	doJSON(t, http.MethodGet, amModelsURL(tt, providerID), tt.AccessToken, nil, &out)
	return out.Models
}

func amFind(t *testing.T, models []amProviderModel, id string) amProviderModel {
	t.Helper()
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	require.Failf(t, "model not listed", "id %s not in %+v", id, models)
	return amProviderModel{}
}

func amDefault(t *testing.T, models []amProviderModel) amProviderModel {
	t.Helper()
	var found []amProviderModel
	for _, m := range models {
		if m.IsDefault {
			found = append(found, m)
		}
	}
	require.Lenf(t, found, 1, "expected exactly one default model in %+v", models)
	return found[0]
}

func amAdd(t *testing.T, tt *helpers.TestTenant, providerID, name, displayName string) amProviderModel {
	t.Helper()
	body := map[string]any{"name": name}
	if displayName != "" {
		body["displayName"] = displayName
	}
	var out amProviderModel
	doJSON(t, http.MethodPost, amModelsURL(tt, providerID), tt.AccessToken, body, &out)
	require.NotEmpty(t, out.ID)
	return out
}

// amExpect sends a request and asserts the refusal status and code.
func amExpect(t *testing.T, method, url, token string, body any, wantStatus int, wantCode string) {
	t.Helper()
	status, raw := doJSONStatus(t, method, url, token, body)
	requireDenied(t, status, raw, wantStatus, wantCode, method+" "+url)
}

func amPatchModel(t *testing.T, tt *helpers.TestTenant, providerID, modelID string, body any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch, amModelsURL(tt, providerID)+"/"+modelID, tt.AccessToken, body)
}

func amPutDefault(t *testing.T, tt *helpers.TestTenant, providerID, modelID string) {
	t.Helper()
	var out struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPut, amBase(tt)+"/providers/"+providerID+"/default-model",
		tt.AccessToken, map[string]any{"modelId": modelID}, &out)
	require.True(t, out.Ok)
}

// amCreateAgent creates an agent on the model and returns its public id.
func amCreateAgent(t *testing.T, tt *helpers.TestTenant, modelID string, temperature *int) string {
	t.Helper()
	body := map[string]any{
		"modelId":      modelID,
		"name":         "Model Fixture Agent",
		"systemPrompt": "You are a fixture agent.",
	}
	if temperature != nil {
		body["temperature"] = *temperature
	}
	var out struct {
		ID      string `json:"id"`
		ModelID string `json:"modelId"`
	}
	doJSON(t, http.MethodPost, amBase(tt)+"/agents", tt.AccessToken, body, &out)
	require.NotEmpty(t, out.ID)
	require.Equal(t, modelID, out.ModelID)
	return out.ID
}

func amPatchAgentModel(t *testing.T, token string, tt *helpers.TestTenant, agentID, modelID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch, amBase(tt)+"/agents/"+agentID+"/model", token,
		map[string]any{"modelId": modelID})
}

func amRequireAgentModelOK(t *testing.T, tt *helpers.TestTenant, agentID, modelID string) {
	t.Helper()
	status, raw := amPatchAgentModel(t, tt.AccessToken, tt, agentID, modelID)
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)
	var out struct {
		Ok bool `json:"ok"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.True(t, out.Ok)
}

// amAgentModelID reads the agent's model public id through the list API.
func amAgentModelID(t *testing.T, tt *helpers.TestTenant, agentID string) string {
	t.Helper()
	var out struct {
		Agents []struct {
			ID      string `json:"id"`
			ModelID string `json:"modelId"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, amBase(tt)+"/agents?limit=200", tt.AccessToken, nil, &out)
	for _, a := range out.Agents {
		if a.ID == agentID {
			return a.ModelID
		}
	}
	require.Failf(t, "agent not listed", "id %s", agentID)
	return ""
}

// amAgentTemperature reads ai_agents.temperature (x100), which the API does not expose.
func amAgentTemperature(t *testing.T, agentID string) int {
	t.Helper()
	var temp int
	require.NoError(t, testDB.QueryRow(
		`SELECT temperature FROM ai_agents WHERE public_id = UUID_TO_BIN(?, 0)`, agentID,
	).Scan(&temp))
	return temp
}

func amProviderDefaultModel(t *testing.T, providerID string) sql.NullString {
	t.Helper()
	var dm sql.NullString
	require.NoError(t, testDB.QueryRow(
		`SELECT default_model FROM ai_providers WHERE public_id = UUID_TO_BIN(?, 0)`, providerID,
	).Scan(&dm))
	return dm
}

// amPickerModels lists GET /ai/models (the cross-provider picker).
func amPickerModels(t *testing.T, tt *helpers.TestTenant) []struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	ProviderID   string `json:"providerId"`
	ProviderKind string `json:"providerKind"`
	ProviderName string `json:"providerName"`
} {
	t.Helper()
	var out struct {
		Total  int64 `json:"total"`
		Models []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			DisplayName  string `json:"displayName"`
			ProviderID   string `json:"providerId"`
			ProviderKind string `json:"providerKind"`
			ProviderName string `json:"providerName"`
		} `json:"models"`
	}
	doJSON(t, http.MethodGet, amBase(tt)+"/models?limit=200", tt.AccessToken, nil, &out)
	require.Equal(t, int64(len(out.Models)), out.Total)
	return out.Models
}

func TestAIModelAddAppearsInPicker(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	provID := amAnthropic(t, tt)

	created := amAdd(t, tt, provID, "  claude-extra-1  ", "  Extra One  ")
	require.Equal(t, "claude-extra-1", created.Name, "name is stored trimmed")
	require.Equal(t, "Extra One", created.DisplayName, "displayName is stored trimmed")
	require.True(t, created.Enabled)
	require.False(t, created.IsDefault)
	require.Equal(t, 0, created.AgentCount)

	var found bool
	for _, m := range amPickerModels(t, tt) {
		if m.ID != created.ID {
			continue
		}
		found = true
		require.Equal(t, "claude-extra-1", m.Name)
		require.Equal(t, "Extra One", m.DisplayName)
		require.Equal(t, provID, m.ProviderID)
		require.Equal(t, "anthropic", m.ProviderKind)
		require.Equal(t, "Anthropic Fixture", m.ProviderName)
	}
	require.True(t, found, "added model must appear in GET /ai/models")
	require.Equal(t, created, amFind(t, amList(t, tt, provID), created.ID))

	// A name omitted displayName falls back to the name.
	bare := amAdd(t, tt, provID, "claude-extra-2", "")
	require.Equal(t, "claude-extra-2", bare.DisplayName)

	url := amModelsURL(tt, provID)
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "claude-extra-1"},
		http.StatusConflict, "AI.MODEL.ALREADY_EXISTS")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "has space"},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "   "},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "model-é"},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": strings.Repeat("m", 129)},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"displayName": "no name"},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_MISSING")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "ok-name", "displayName": "  "},
		http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")

	// Boundaries that are accepted.
	amAdd(t, tt, provID, "m", "")
	amAdd(t, tt, provID, strings.Repeat("n", 128), "")
	amAdd(t, tt, provID, "Qwen/Qwen2.5-7B", "")
	amAdd(t, tt, provID, "llama3.2:8b", "")
}

func TestAIModelPatchMatrix(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const renamed = "Renamed Model"
	cases := []struct {
		name           string
		enabledInput   string // "true", "false", "omit"
		setName        bool
		currentEnabled bool
		isDefault      bool
		inUse          bool
		wantStatus     int
		wantCode       string
		wantEnabled    bool
		wantRenamed    bool
	}{
		{"disable_in_use_with_name", "false", true, true, false, true, http.StatusConflict, "AI.MODEL.IN_USE", true, false},
		{"enable_already_enabled_default", "true", false, true, true, false, http.StatusOK, "", true, false},
		{"rename_default_in_use", "omit", true, true, true, true, http.StatusOK, "", true, true},
		{"disable_default_in_use", "false", false, true, true, true, http.StatusConflict, "AI.MODEL.IS_PROVIDER_DEFAULT", true, false},
		{"rename_disabled", "omit", true, false, false, false, http.StatusOK, "", false, true},
		{"disable_already_disabled", "false", false, false, false, false, http.StatusOK, "", false, false},
		{"reenable_with_name", "true", true, false, false, false, http.StatusOK, "", true, true},
		{"enable_enabled_in_use_with_name", "true", true, true, false, true, http.StatusOK, "", true, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tt := newTenant(t)
			provID := amAnthropic(t, tt)
			def := amDefault(t, amList(t, tt, provID))
			extra := amAdd(t, tt, provID, "matrix-extra", "Matrix Extra")

			target := extra
			if tc.isDefault {
				target = def
			}
			if !tc.currentEnabled {
				status, raw := amPatchModel(t, tt, provID, target.ID, map[string]any{"enabled": false})
				require.Equalf(t, http.StatusOK, status, "setup disable: %s", raw)
			}
			if tc.inUse {
				amCreateAgent(t, tt, target.ID, nil)
			}
			before := amFind(t, amList(t, tt, provID), target.ID)
			require.Equal(t, tc.currentEnabled, before.Enabled)
			require.Equal(t, tc.isDefault, before.IsDefault)
			require.Equal(t, tc.inUse, before.AgentCount > 0)

			body := map[string]any{}
			switch tc.enabledInput {
			case "true":
				body["enabled"] = true
			case "false":
				body["enabled"] = false
			}
			if tc.setName {
				body["displayName"] = renamed
			}
			status, raw := amPatchModel(t, tt, provID, target.ID, body)
			if tc.wantCode != "" {
				requireDenied(t, status, raw, tc.wantStatus, tc.wantCode, tc.name)
			} else {
				require.Equalf(t, tc.wantStatus, status, "body=%s", raw)
				var out struct {
					Ok bool `json:"ok"`
				}
				require.NoError(t, json.Unmarshal(raw, &out))
				require.True(t, out.Ok)
			}

			after := amFind(t, amList(t, tt, provID), target.ID)
			require.Equal(t, tc.wantEnabled, after.Enabled)
			wantName := before.DisplayName
			if tc.wantRenamed {
				wantName = renamed
			}
			require.Equal(t, wantName, after.DisplayName)
			require.Equal(t, before.IsDefault, after.IsDefault)
			require.Equal(t, before.AgentCount, after.AgentCount)
		})
	}

	t.Run("empty_body", func(t *testing.T) {
		t.Parallel()
		tt := newTenant(t)
		provID := amAnthropic(t, tt)
		extra := amAdd(t, tt, provID, "matrix-empty", "Matrix Empty")
		url := amModelsURL(tt, provID) + "/" + extra.ID
		amExpect(t, http.MethodPatch, url, tt.AccessToken, map[string]any{},
			http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
		amExpect(t, http.MethodPatch, url, tt.AccessToken, map[string]any{"enabled": nil, "displayName": nil},
			http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID")
		require.Equal(t, extra, amFind(t, amList(t, tt, provID), extra.ID))
	})
}

func TestAIModelSetDefault(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	provID := amAnthropic(t, tt)
	initial := amDefault(t, amList(t, tt, provID))
	require.Equal(t, initial.Name, amProviderDefaultModel(t, provID).String)

	other := amAdd(t, tt, provID, "claude-other", "Other")
	require.False(t, other.IsDefault, "a provider that already has a default keeps it")

	amPutDefault(t, tt, provID, other.ID)
	models := amList(t, tt, provID)
	require.True(t, amFind(t, models, other.ID).IsDefault)
	require.False(t, amFind(t, models, initial.ID).IsDefault)
	dm := amProviderDefaultModel(t, provID)
	require.True(t, dm.Valid)
	require.Equal(t, "claude-other", dm.String)

	// Setting the current default again is a no-op 200.
	amPutDefault(t, tt, provID, other.ID)
	require.Equal(t, other.ID, amDefault(t, amList(t, tt, provID)).ID)

	putURL := amBase(tt) + "/providers/" + provID + "/default-model"

	// A disabled model cannot become the default.
	status, raw := amPatchModel(t, tt, provID, initial.ID, map[string]any{"enabled": false})
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)
	amExpect(t, http.MethodPut, putURL, tt.AccessToken, map[string]any{"modelId": initial.ID},
		http.StatusNotFound, "AI.MODEL.NOT_FOUND")

	// A model of another provider is not found under this one.
	otherProv := amAnthropic(t, tt)
	foreign := amDefault(t, amList(t, tt, otherProv))
	amExpect(t, http.MethodPut, putURL, tt.AccessToken, map[string]any{"modelId": foreign.ID},
		http.StatusNotFound, "AI.MODEL.NOT_FOUND")

	amExpect(t, http.MethodPut, putURL, tt.AccessToken, map[string]any{"modelId": "not-a-uuid"},
		http.StatusBadRequest, "VALIDATION.PATH.PARAM_INVALID")

	require.Equal(t, "claude-other", amProviderDefaultModel(t, provID).String, "refusals leave the default alone")
}

// amUpstream is a fake OpenAI-compatible endpoint that records the "model"
// of the last chat completion request and refuses it with a 401 (which the
// provider does not retry).
type amUpstream struct {
	srv       *httptest.Server
	lastModel atomic.Value
	hits      atomic.Int32
}

func amNewUpstream(t *testing.T) *amUpstream {
	t.Helper()
	u := &amUpstream{}
	u.lastModel.Store("")
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		u.lastModel.Store(body.Model)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_api_key"}}`))
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *amUpstream) model() string { return u.lastModel.Load().(string) }

func TestAIModelDefaultModelDrivesAICalls(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	if amMockMode() {
		// The mock resolver ignores the provider, so only persistence is observable.
		tt := newTenant(t)
		provID := amAnthropic(t, tt)
		alt := amAdd(t, tt, provID, "claude-alt-1", "")
		amPutDefault(t, tt, provID, alt.ID)
		require.Equal(t, "claude-alt-1", amProviderDefaultModel(t, provID).String)
		require.Equal(t, alt.ID, amDefault(t, amList(t, tt, provID)).ID)
		return
	}

	tt := newTenant(t)
	older := amNewUpstream(t)
	newer := amNewUpstream(t)
	olderProv := amOpenAICompat(t, tt, older.srv.URL, "older-default")
	newerProv := amOpenAICompat(t, tt, newer.srv.URL, "newer-default") // newest provider is the workspace default

	// PUT on the older provider changes nothing for AI calls.
	olderAlt := amAdd(t, tt, olderProv, "older-alt", "")
	amPutDefault(t, tt, olderProv, olderAlt.ID)
	status, raw := postCompileLensStatus(t, tt, "blocked tasks due this week, older put")
	require.NotEqualf(t, http.StatusOK, status, "the fake upstream refuses, body=%s", raw)
	require.Equal(t, int32(0), older.hits.Load(), "the older provider must not serve AI calls")
	require.Equal(t, "newer-default", newer.model())

	// PUT on the newest provider is what the upstream sees.
	newerAlt := amAdd(t, tt, newerProv, "newer-alt", "")
	amPutDefault(t, tt, newerProv, newerAlt.ID)
	postCompileLensStatus(t, tt, "blocked tasks due this week, newer put")
	require.Equal(t, "newer-alt", newer.model())
	require.Equal(t, int32(0), older.hits.Load())

	// Switching back is followed too.
	require.True(t, amFind(t, amList(t, tt, newerProv), newerAlt.ID).IsDefault)
	for _, m := range amList(t, tt, newerProv) {
		if m.Name == "newer-default" {
			amPutDefault(t, tt, newerProv, m.ID)
		}
	}
	postCompileLensStatus(t, tt, "blocked tasks due this week, back")
	require.Equal(t, "newer-default", newer.model())
}

// postCompileLensStatus wraps postCompileLens for callers that only need the
// status and body.
func postCompileLensStatus(t *testing.T, tt *helpers.TestTenant, prompt string) (int, []byte) {
	t.Helper()
	status, _, raw := postCompileLens(t, tt, prompt)
	return status, raw
}

func TestAIModelAgentRepoint(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	compatProv := amOpenAICompat(t, tt, "http://127.0.0.1:9/v1", "gpt-repoint")
	compat := amDefault(t, amList(t, tt, compatProv))
	anthropicProv := amAnthropic(t, tt)
	claude := amDefault(t, amList(t, tt, anthropicProv))

	hot, mild := 150, 50
	hotAgent := amCreateAgent(t, tt, compat.ID, &hot)
	mildAgent := amCreateAgent(t, tt, compat.ID, &mild)
	require.Equal(t, 150, amAgentTemperature(t, hotAgent))
	require.Equal(t, 50, amAgentTemperature(t, mildAgent))

	amRequireAgentModelOK(t, tt, hotAgent, claude.ID)
	amRequireAgentModelOK(t, tt, mildAgent, claude.ID)
	require.Equal(t, claude.ID, amAgentModelID(t, tt, hotAgent))
	require.Equal(t, claude.ID, amAgentModelID(t, tt, mildAgent))
	require.Equal(t, 100, amAgentTemperature(t, hotAgent), "Anthropic caps temperature at 100")
	require.Equal(t, 50, amAgentTemperature(t, mildAgent), "a value under the cap is kept")

	// Repointing to the current model is a plain 200; moving back keeps the capped value.
	amRequireAgentModelOK(t, tt, hotAgent, claude.ID)
	amRequireAgentModelOK(t, tt, hotAgent, compat.ID)
	require.Equal(t, compat.ID, amAgentModelID(t, tt, hotAgent))
	require.Equal(t, 100, amAgentTemperature(t, hotAgent))

	// The usage counts follow the move.
	require.Equal(t, 1, amFind(t, amList(t, tt, compatProv), compat.ID).AgentCount)
	require.Equal(t, 1, amFind(t, amList(t, tt, anthropicProv), claude.ID).AgentCount)

	base := amBase(tt) + "/agents/" + hotAgent + "/model"
	amExpect(t, http.MethodPatch, base, tt.AccessToken, map[string]any{"modelId": uuid.Must(uuid.NewV7()).String()},
		http.StatusNotFound, "AI.MODEL.NOT_FOUND")
	amExpect(t, http.MethodPatch, base, tt.AccessToken, map[string]any{"modelId": "not-a-uuid"},
		http.StatusBadRequest, "VALIDATION.PATH.PARAM_INVALID")
	amExpect(t, http.MethodPatch,
		amBase(tt)+"/agents/"+uuid.Must(uuid.NewV7()).String()+"/model", tt.AccessToken,
		map[string]any{"modelId": claude.ID}, http.StatusNotFound, "AI.AGENT.NOT_FOUND")
}

func TestAIModelStrandedAgentRepoint(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	strandedProv := amAnthropic(t, tt)
	stranded := amDefault(t, amList(t, tt, strandedProv))
	liveProv := amOpenAICompat(t, tt, "http://127.0.0.1:9/v1", "gpt-live")
	live := amDefault(t, amList(t, tt, liveProv))

	agentID := amCreateAgent(t, tt, stranded.ID, nil)

	// The API refuses to delete a provider an agent uses, so disable it directly.
	res, err := testDB.Exec(`UPDATE ai_providers SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, strandedProv)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	for _, m := range amPickerModels(t, tt) {
		require.NotEqual(t, stranded.ID, m.ID, "a disabled provider's model is not offered")
	}

	// The current model can be re-specified, then the agent moves to a live one.
	amRequireAgentModelOK(t, tt, agentID, stranded.ID)
	require.Equal(t, stranded.ID, amAgentModelID(t, tt, agentID))
	amRequireAgentModelOK(t, tt, agentID, live.ID)
	require.Equal(t, live.ID, amAgentModelID(t, tt, agentID))

	// And it cannot go back to the disabled provider.
	amExpect(t, http.MethodPatch, amBase(tt)+"/agents/"+agentID+"/model", tt.AccessToken,
		map[string]any{"modelId": stranded.ID}, http.StatusNotFound, "AI.MODEL.NOT_FOUND")
}

func TestAIModelDisabledProviderRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	goneProv := amAnthropic(t, tt)
	gone := amDefault(t, amList(t, tt, goneProv))
	liveProv := amOpenAICompat(t, tt, "http://127.0.0.1:9/v1", "gpt-live")
	live := amDefault(t, amList(t, tt, liveProv))

	liveAgent := amCreateAgent(t, tt, live.ID, nil)

	var ack struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete, amBase(tt)+"/providers/"+goneProv, tt.AccessToken, nil, &ack)

	amExpect(t, http.MethodPost, amBase(tt)+"/agents", tt.AccessToken, map[string]any{
		"modelId":      gone.ID,
		"name":         "Rejected Agent",
		"systemPrompt": "x",
	}, http.StatusNotFound, "AI.MODEL.NOT_FOUND")
	amExpect(t, http.MethodPatch, amBase(tt)+"/agents/"+liveAgent+"/model", tt.AccessToken,
		map[string]any{"modelId": gone.ID}, http.StatusNotFound, "AI.MODEL.NOT_FOUND")
	require.Equal(t, live.ID, amAgentModelID(t, tt, liveAgent), "a refused repoint changes nothing")

	// The provider itself is gone for the model routes.
	amExpect(t, http.MethodGet, amModelsURL(tt, goneProv), tt.AccessToken, nil,
		http.StatusNotFound, "AI.PROVIDER.NOT_FOUND")
	amExpect(t, http.MethodPost, amModelsURL(tt, goneProv), tt.AccessToken, map[string]any{"name": "late-model"},
		http.StatusNotFound, "AI.PROVIDER.NOT_FOUND")
}

func TestAIModelProviderDeleteInUse(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	provID := amAnthropic(t, tt)
	model := amDefault(t, amList(t, tt, provID))
	otherProv := amOpenAICompat(t, tt, "http://127.0.0.1:9/v1", "gpt-other")
	other := amDefault(t, amList(t, tt, otherProv))

	agentID := amCreateAgent(t, tt, model.ID, nil)
	delURL := amBase(tt) + "/providers/" + provID

	amExpect(t, http.MethodDelete, delURL, tt.AccessToken, nil, http.StatusConflict, "AI.PROVIDER.IN_USE")
	require.Equal(t, 1, amFind(t, amList(t, tt, provID), model.ID).AgentCount, "the refused delete leaves the provider usable")

	amRequireAgentModelOK(t, tt, agentID, other.ID)
	var ack struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete, delURL, tt.AccessToken, nil, &ack)
	amExpect(t, http.MethodGet, amModelsURL(tt, provID), tt.AccessToken, nil,
		http.StatusNotFound, "AI.PROVIDER.NOT_FOUND")
}

func TestAIModelLimitReached(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	provID := amAnthropic(t, tt) // one enabled model

	var wsID, provInternal uint32
	require.NoError(t, testDB.QueryRow(
		`SELECT workspace_id, id FROM ai_providers WHERE public_id = UUID_TO_BIN(?, 0)`, provID,
	).Scan(&wsID, &provInternal))

	const seeded = 198
	var sb strings.Builder
	args := make([]any, 0, seeded*5)
	sb.WriteString(`INSERT INTO ai_models (
		public_id, workspace_id, provider_id, name, display_name,
		context_window, max_output_tokens,
		input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
		supports_tools, supports_vision, enabled
	) VALUES `)
	for i := 0; i < seeded; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?, ?, ?, ?, ?, 128000, 4096, 0, 0, FALSE, FALSE, TRUE)")
		name := fmt.Sprintf("seed-model-%03d", i)
		args = append(args, types.New(), wsID, provInternal, name, name)
	}
	_, err := testDB.Exec(sb.String(), args...)
	require.NoError(t, err)

	url := amModelsURL(tt, provID)

	last := amAdd(t, tt, provID, "model-200th", "") // 199 + 1 = 200
	require.True(t, last.Enabled)

	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "model-201st"},
		http.StatusConflict, "AI.MODEL.LIMIT_REACHED")
	// The limit check runs before the duplicate check.
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "model-200th"},
		http.StatusConflict, "AI.MODEL.LIMIT_REACHED")
	amExpect(t, http.MethodPost, amBase(tt)+"/providers", tt.AccessToken, map[string]any{
		"kind":         "openai_compat",
		"name":         "Over The Limit",
		"baseUrl":      "http://127.0.0.1:9/v1",
		"defaultModel": "gpt-over",
		"apiKey":       amFixtureKey,
	}, http.StatusConflict, "AI.MODEL.LIMIT_REACHED")

	// Free a slot, take it, and a disabled model can no longer come back.
	status, raw := amPatchModel(t, tt, provID, last.ID, map[string]any{"enabled": false})
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)
	amAdd(t, tt, provID, "model-refill", "")
	amExpect(t, http.MethodPatch, url+"/"+last.ID, tt.AccessToken, map[string]any{"enabled": true},
		http.StatusConflict, "AI.MODEL.LIMIT_REACHED")
	amExpect(t, http.MethodPost, url, tt.AccessToken, map[string]any{"name": "model-200th"},
		http.StatusConflict, "AI.MODEL.LIMIT_REACHED")
}

func TestAIModelNullDefaultProvider(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	seeded := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	var provID string
	require.NoError(t, testDB.QueryRow(
		`SELECT BIN_TO_UUID(public_id, 0) FROM ai_providers WHERE id = ?`, seeded.ProviderID,
	).Scan(&provID))
	require.False(t, amProviderDefaultModel(t, provID).Valid, "the seeded provider has no default model")

	models := amList(t, tt, provID)
	require.Len(t, models, 1)
	require.Equal(t, seeded.ModelPublicID, models[0].ID)
	require.False(t, models[0].IsDefault)
	require.Equal(t, 1, models[0].AgentCount)

	// The first model added to a provider with no default becomes its default.
	added := amAdd(t, tt, provID, "first-added", "")
	require.True(t, added.IsDefault)
	models = amList(t, tt, provID)
	require.True(t, amFind(t, models, added.ID).IsDefault)
	require.False(t, amFind(t, models, seeded.ModelPublicID).IsDefault)
	require.Equal(t, "first-added", amProviderDefaultModel(t, provID).String)
}

func TestAIModelMemberForbidden(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	admin := newTenant(t)
	member := inviteMember(t, admin)
	provID := amAnthropic(t, admin)
	model := amDefault(t, amList(t, admin, provID))
	extra := amAdd(t, admin, provID, "member-extra", "")

	const denied = "WS.MEMBER.ROLE_DENIED"
	forbid := func(method, url string, body any) {
		t.Helper()
		amExpect(t, method, url, member.AccessToken, body, http.StatusForbidden, denied)
	}
	pBase := amBase(admin) + "/providers/" + provID
	forbid(http.MethodGet, pBase+"/models", nil)
	forbid(http.MethodPost, pBase+"/models", map[string]any{"name": "member-added"})
	forbid(http.MethodPatch, pBase+"/models/"+extra.ID, map[string]any{"displayName": "Hijacked"})
	forbid(http.MethodPut, pBase+"/default-model", map[string]any{"modelId": extra.ID})
	forbid(http.MethodGet, pBase+"/available-models", nil)

	models := amList(t, admin, provID)
	require.Len(t, models, 2, "no model was added by the member")
	require.Equal(t, model.ID, amDefault(t, models).ID)
	require.Equal(t, "member-extra", amFind(t, models, extra.ID).DisplayName)

	// Picking among the admin's models is a member operation.
	agentID := amCreateAgent(t, admin, model.ID, nil)
	status, raw := amPatchAgentModel(t, member.AccessToken, admin, agentID, extra.ID)
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)
	require.Equal(t, extra.ID, amAgentModelID(t, admin, agentID))
}

func TestAIModelReenableCase(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	provID := amAnthropic(t, tt)
	created := amAdd(t, tt, provID, "Case-Model", "Case Display")
	countBefore := len(amList(t, tt, provID))

	status, raw := amPatchModel(t, tt, provID, created.ID, map[string]any{"enabled": false})
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)

	// Same name apart from case: the disabled row comes back under the new spelling.
	again := amAdd(t, tt, provID, "case-model", "")
	require.Equal(t, created.ID, again.ID, "no second row is created")
	require.Equal(t, "case-model", again.Name)
	require.True(t, again.Enabled)
	require.Equal(t, "Case Display", again.DisplayName, "displayName is kept when none is given")
	require.Equal(t, countBefore, len(amList(t, tt, provID)))

	// While it is enabled, any spelling is a duplicate.
	amExpect(t, http.MethodPost, amModelsURL(tt, provID), tt.AccessToken, map[string]any{"name": "CASE-MODEL"},
		http.StatusConflict, "AI.MODEL.ALREADY_EXISTS")

	// An explicit displayName replaces the old one on re-enable.
	status, raw = amPatchModel(t, tt, provID, created.ID, map[string]any{"enabled": false})
	require.Equalf(t, http.StatusOK, status, "body=%s", raw)
	third := amAdd(t, tt, provID, "CASE-MODEL", "New Display")
	require.Equal(t, created.ID, third.ID)
	require.Equal(t, "CASE-MODEL", third.Name)
	require.Equal(t, "New Display", third.DisplayName)
}

func TestAIModelAvailableModels(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	amExpect(t, http.MethodGet,
		amBase(tt)+"/providers/"+uuid.Must(uuid.NewV7()).String()+"/available-models", tt.AccessToken, nil,
		http.StatusNotFound, "AI.PROVIDER.NOT_FOUND")

	if amMockMode() {
		provID := amAnthropic(t, tt)
		var out struct {
			Models []string `json:"models"`
		}
		doJSON(t, http.MethodGet, amBase(tt)+"/providers/"+provID+"/available-models", tt.AccessToken, nil, &out)
		require.NotNil(t, out.Models)
		require.Empty(t, out.Models)
		return
	}

	var gotAuth atomic.Value
	gotAuth.Store("")
	var gotPath atomic.Value
	gotPath.Store("")
	var mode atomic.Int32 // 0 = list, 1 = reject
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotPath.Store(r.URL.Path)
		if mode.Load() == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"zeta-model"},{"id":"alpha-model"},{"id":"alpha-model"},{"id":"has space"},{"id":"Qwen/Qwen2.5-7B"}]}`))
	}))
	t.Cleanup(upstream.Close)

	provID := amOpenAICompat(t, tt, upstream.URL, "alpha-model")
	url := amBase(tt) + "/providers/" + provID + "/available-models"

	var out struct {
		Models []string `json:"models"`
	}
	doJSON(t, http.MethodGet, url, tt.AccessToken, nil, &out)
	require.Equal(t, []string{"Qwen/Qwen2.5-7B", "alpha-model", "zeta-model"}, out.Models,
		"IDs are de-duplicated, sorted, and names the model-name rule refuses are dropped")
	require.Equal(t, "/models", gotPath.Load().(string))
	require.Equal(t, "Bearer "+amFixtureKey, gotAuth.Load().(string))

	mode.Store(1)
	amExpect(t, http.MethodGet, url, tt.AccessToken, nil,
		http.StatusBadGateway, "AI.PROVIDER.UPSTREAM_AUTH_REJECTED")
}
