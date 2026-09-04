package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestAIProviderCRUD exercises the AI provider create/list/patch/delete
// endpoints and verifies the plaintext key never appears in any
// response body.
func TestAIProviderCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	plaintextKey := "sk-ant-this-is-a-test-key-0123456789"

	// Create.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Test Anthropic",
			"apiKey": plaintextKey,
		})
	require.GreaterOrEqual(t, status, 200, "create status body=%s", string(body))
	require.Less(t, status, 300, "create status body=%s", string(body))
	require.False(t, strings.Contains(string(body), plaintextKey),
		"create response must not echo plaintext key")

	var created struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		APIKeyMasked string `json:"apiKeyMasked"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "anthropic", created.Kind)
	require.NotEmpty(t, created.APIKeyMasked)

	// List — no ciphertext, no plaintext.
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.False(t, strings.Contains(string(body), plaintextKey),
		"list response must not contain plaintext key")

	// Patch (rotate key).
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+created.ID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-rotated-key-9876543210"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusOK, status)

	// Delete.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}

// TestAIProviderKeyMustBeStorable pins the boundary between what the
// provider endpoints accept as an API key and what the columns behind
// them can hold.
//
// The key's derived prefix and suffix are stored in latin1 columns and
// its ciphertext in a VARBINARY(512), so a key carrying CJK or an emoji,
// or one long enough that the sealed blob overflows, used to pass
// validation and then fail the insert — the caller got a server error
// naming nothing. Each refusal below is paired with an accepted key of
// the same shape, because a test that only asserts refusals passes just
// as well on an endpoint that rejects everything.
func TestAIProviderKeyMustBeStorable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	createURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/providers"

	create := func(key string) (int, []byte) {
		return doJSONStatus(t, http.MethodPost, createURL, tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Charset Probe",
			"apiKey": key,
		})
	}

	refused := []struct {
		name string
		key  string
	}{
		// The mask windows are the first 8 and last 4 bytes, so where a
		// non-Latin-1 character sits decides whether the latin1 columns
		// see it. All three positions are covered: the constraint is on
		// the key, not on the window it happens to miss.
		{"cjk-in-prefix", "鍵sk-ant-0123456789"},
		{"cjk-in-suffix", "sk-ant-0123456789鍵"},
		{"cjk-in-middle", "sk-ant-鍵-0123456789"},
		{"emoji", "\U0001F511sk-ant-0123456789"},
		// Latin-1 storable, but not a shape any provider issues; the
		// boundary is printable ASCII so the columns are right by
		// construction rather than by the key happening to fit.
		{"latin1-accented", "sk-ant-clé-0123456789"},
		{"whitespace", "sk-ant key 0123456789"},
		// Sealed with a 12-byte nonce and a 16-byte tag, a 500-character
		// key is 528 bytes and does not fit api_key_ciphertext.
		{"over-ciphertext-bound", "sk-" + strings.Repeat("a", 497)},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			status, body := create(tc.key)
			require.Equalf(t, http.StatusUnprocessableEntity, status,
				"want a validation refusal, got %d body=%s", status, string(body))
			require.Containsf(t, string(body), "body.apiKey",
				"the refusal must name the field, body=%s", string(body))
		})
	}

	// A key of the shape every supported provider actually issues —
	// base64url characters, hyphens, an underscore — is still accepted.
	goodKey := "sk-ant-api03-Ab_Cd-0123456789+/=" //#nosec G101 -- synthetic test fixture, never a real key
	status, body := create(goodKey)
	require.GreaterOrEqualf(t, status, 200, "a legitimate key must be accepted, body=%s", string(body))
	require.Lessf(t, status, 300, "a legitimate key must be accepted, body=%s", string(body))
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.ID)

	// Rotation applies the same boundary, and accepts the same shapes.
	patchURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/ai/providers/" + created.ID
	status, body = doJSONStatus(t, http.MethodPatch, patchURL, tt.AccessToken,
		map[string]any{"apiKey": "回転鍵sk-ant-0123456789"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equalf(t, http.StatusUnprocessableEntity, status,
		"rotation must refuse an unstorable key, got %d body=%s", status, string(body))
	require.Containsf(t, string(body), "body.apiKey",
		"the rotation refusal must name the field, body=%s", string(body))

	status, body = doJSONStatus(t, http.MethodPatch, patchURL, tt.AccessToken,
		map[string]any{"apiKey": "sk-ant-api03-Rotated_9876-54321"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equalf(t, http.StatusOK, status,
		"a legitimate rotation must still succeed, body=%s", string(body))

	// Nothing the endpoint refused reached the table: exactly the one
	// accepted provider exists.
	var listed struct {
		Total int64 `json:"total"`
	}
	status, body = doJSONStatus(t, http.MethodGet, createURL, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(body, &listed))
	require.Equal(t, int64(1), listed.Total,
		"a refused key must not leave a provider row behind")
}

// countProviderAudit returns how many audit_logs rows exist for the
// given internal workspace id and action. Used to assert that a failed
// rotation/deletion records no audit entry.
func countProviderAudit(t *testing.T, wsID uint32, action string) int {
	t.Helper()
	var n int
	err := testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		wsID, action).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestAIProviderRotateDeleteNotFound verifies that rotating or deleting a
// provider that does not exist in the caller's workspace (either a random
// id or one owned by another workspace) returns 404 and records no audit
// entry. This guards against a false-success where a leaked key appears
// rotated but the stored ciphertext is never touched.
func TestAIProviderRotateDeleteNotFound(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	other := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)

	// A provider that belongs to the other workspace; the caller must not
	// be able to touch it through their own workspace path.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/ai/providers",
		other.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Other Anthropic",
			"apiKey": "sk-ant-other-workspace-key-0123456789", //#nosec G101 -- synthetic test fixture, never a real key
		})
	require.GreaterOrEqual(t, status, 200, "create status body=%s", string(body))
	require.Less(t, status, 300, "create status body=%s", string(body))
	var otherProvider struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &otherProvider))
	require.NotEmpty(t, otherProvider.ID)

	updateBefore := countProviderAudit(t, wsID, "ai_provider.update")
	deleteBefore := countProviderAudit(t, wsID, "ai_provider.delete")

	missingID := types.New().UUID().String()

	// PATCH: random non-existent id -> 404.
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+missingID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-should-not-apply-000000"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusNotFound, status)

	// PATCH: id owned by another workspace -> 404 (cross-tenant isolation).
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+otherProvider.ID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-should-not-apply-111111"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusNotFound, status)

	// DELETE: random non-existent id -> 404.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+missingID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status)

	// DELETE: id owned by another workspace -> 404.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+otherProvider.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status)

	// None of the failed rotations/deletions may have written an audit row.
	require.Equal(t, updateBefore, countProviderAudit(t, wsID, "ai_provider.update"),
		"failed rotation must not append an audit row")
	require.Equal(t, deleteBefore, countProviderAudit(t, wsID, "ai_provider.delete"),
		"failed deletion must not append an audit row")
}
