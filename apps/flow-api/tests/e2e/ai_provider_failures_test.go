package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestAIProviderFailures exercises POST /workspaces/{wsId}/ai/compile-lens
// against a per-tenant openai_compat provider whose baseURL points at an
// httptest.Server that simulates each upstream failure mode. Audit M15.
//
// The shared flow-api test server already wires
// providers.WorkspaceResolver when NF_FLOW_AI_MOCK is unset, so each
// sub-test only has to:
//
//  1. Start a fake upstream that returns the desired failure response.
//  2. POST /workspaces/{wsId}/ai/providers with kind=openai_compat and
//     baseUrl=<fake>. The newest provider wins because
//     ListProvidersForWorkspace orders by created_at DESC LIMIT 1.
//  3. POST /workspaces/{wsId}/ai/compile-lens and assert the resulting
//     status + AI.* error code propagated by the sentinels in
//     internal/ai/providers/errors.go.
//
// Each sub-test owns its own tenant + fake server so they all run in
// parallel without sharing state with the happy-path
// TestAiCompileLensHappyAndSad (which is gated on NF_FLOW_AI_MOCK=1 and
// uses the deterministic mock provider).
func TestAIProviderFailures(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false" {
		t.Skip("NF_FLOW_AI_MOCK is set; the workspace provider path is bypassed and these failure-mode assertions do not apply")
	}

	type expect struct {
		status     int
		typeCode   string
		retryAfter string
	}

	cases := []struct {
		name     string
		handler  http.HandlerFunc
		expected expect
	}{
		{
			name: "ProviderReturns503",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream down","type":"unavailable"}}`))
			},
			expected: expect{
				status:   http.StatusBadGateway,
				typeCode: "AI.PROVIDER.UPSTREAM_REQUEST_REJECTED",
			},
		},
		{
			name: "ProviderReturns401",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_api_key"}}`))
			},
			expected: expect{
				status:   http.StatusBadGateway,
				typeCode: "AI.PROVIDER.UPSTREAM_AUTH_REJECTED",
			},
		},
		{
			name: "ProviderReturns429",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				// Retry-After=1 keeps the in-process retry budget below
				// 4s total (1s + 2s + 4s exponential fallback would be
				// 7s if Retry-After were absent).
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_exceeded"}}`))
			},
			expected: expect{
				status:     http.StatusTooManyRequests,
				typeCode:   "AI.PROVIDER.UPSTREAM_RATE_LIMITED",
				retryAfter: "1",
			},
		},
		{
			name: "ProviderReturnsMalformedJSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<<<not even json>>>`))
			},
			expected: expect{
				status:   http.StatusBadGateway,
				typeCode: "AI.RESPONSE.INVALID_JSON",
			},
		},
		{
			name: "ProviderReturnsValidJSONButWrongSchema",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				// Valid JSON but missing the OpenAI envelope: no
				// `choices` array, no `error` block.
				_, _ = w.Write([]byte(`{"foo":"bar","baz":42}`))
			},
			expected: expect{
				status:   http.StatusBadGateway,
				typeCode: "AI.RESPONSE.SCHEMA_MISMATCH",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tc.handler(w, r)
			}))
			t.Cleanup(upstream.Close)

			tt := newTenant(t)
			registerOpenAICompatProvider(t, tt, upstream.URL)

			status, headers, raw := postCompileLens(t, tt, "blocked tasks due this week")
			require.Equal(t, tc.expected.status, status,
				"%s: expected status %d, got %d body=%s",
				tc.name, tc.expected.status, status, string(raw))
			require.Truef(t, strings.Contains(string(raw), tc.expected.typeCode),
				"%s: response body must contain error code %q, got=%s",
				tc.name, tc.expected.typeCode, string(raw))
			if tc.expected.retryAfter != "" {
				require.Equal(t, tc.expected.retryAfter, headers.Get("Retry-After"),
					"%s: Retry-After header must propagate from upstream", tc.name)
			}
			require.Greater(t, int(hits.Load()), 0,
				"%s: fake upstream was never called", tc.name)
		})
	}
}

// TestAIProviderFailures_Timeout exercises the upstream-timeout sentinel
// against a fake server that never responds. Lives in its own top-level
// test so the call to providers.SetHTTPTimeoutForTest cannot race with
// the parallel sub-tests in TestAIProviderFailures, which would also use
// providers.sharedClient (audit M15).
func TestAIProviderFailures_Timeout(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") != "" && os.Getenv("NF_FLOW_AI_MOCK") != "0" && os.Getenv("NF_FLOW_AI_MOCK") != "false" {
		t.Skip("NF_FLOW_AI_MOCK is set; the workspace provider path is bypassed")
	}

	var hits atomic.Int32

	// Block long enough that the sharedClient timeout below trips
	// first, then unblock so httptest.Server.Close() does not wedge.
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(upstream.CloseClientConnections)

	// Shrink the package-wide LLM HTTP timeout. SetHTTPTimeoutForTest
	// returns a restore func captured for cleanup so subsequent tests
	// see the production 90s default.
	restore := providers.SetHTTPTimeoutForTest(300 * time.Millisecond)
	t.Cleanup(restore)

	tt := newTenant(t)
	registerOpenAICompatProvider(t, tt, upstream.URL)

	status, _, raw := postCompileLens(t, tt, "blocked tasks due this week")
	require.Equal(t, http.StatusGatewayTimeout, status,
		"timeout: expected 504, got %d body=%s", status, string(raw))
	require.Truef(t, strings.Contains(string(raw), "AI.PROVIDER.UPSTREAM_TIMEOUT"),
		"timeout: response body must contain error code, got=%s", string(raw))
	require.Greater(t, int(hits.Load()), 0, "timeout: fake upstream was never called")
}

// registerOpenAICompatProvider creates an openai_compat AI provider for
// the tenant's workspace pointing at the given baseURL. The new row
// becomes the default because ListProvidersForWorkspace orders by
// created_at DESC LIMIT 1.
func registerOpenAICompatProvider(t *testing.T, tt *helpers.TestTenant, baseURL string) {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken,
		map[string]any{
			"kind":         "openai_compat",
			"name":         "Failure Mode Fixture",
			"baseUrl":      baseURL,
			"defaultModel": "gpt-4o-mini",
			"apiKey":       "sk-test-failure-mode-fixture-0123456789", //#nosec G101 -- synthetic test fixture, never a real key
		},
		&out,
	)
	require.NotEmpty(t, out.ID, "create provider must return a public id")
}

// postCompileLens POSTs to /ai/compile-lens and returns status, response
// headers, and raw body. Used in place of doJSONStatus so failure-mode
// assertions can read the Retry-After header propagated from upstream.
func postCompileLens(t *testing.T, tt *helpers.TestTenant, prompt string) (int, http.Header, []byte) {
	t.Helper()
	url := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/compile-lens"
	body, err := json.Marshal(map[string]any{"prompt": prompt})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tt.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header.Clone(), raw
}
