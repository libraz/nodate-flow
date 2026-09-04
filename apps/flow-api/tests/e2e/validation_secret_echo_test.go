package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefusedAPIKeyIsNotEchoed drives a key that violates the declared
// pattern through the real provider-registration endpoint and holds that
// the refusal does not carry the key back out.
//
// The stock validation envelope answers a refused field with the field's
// value, so tightening the constraints on an API key turned every
// malformed key into a response body containing it — and from there into
// proxy logs, browser history, and any error report the body reaches.
//
// The assertion is against the whole serialised response, not a named
// member: a check on errors[0].value passes just as happily when the
// value reappears under detail or in a message.
func TestRefusedAPIKeyIsNotEchoed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	// A space is outside the printable-ASCII-no-spaces pattern the field
	// declares, so the request is refused before the handler runs.
	const submitted = "sk-ant-rejected key with a space" //#nosec G101 -- synthetic test fixture, never a real key

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Echo Probe",
			"apiKey": submitted,
		})
	body := string(raw)

	require.Equalf(t, http.StatusUnprocessableEntity, status, "body=%s", body)
	require.NotContainsf(t, body, submitted,
		"the refused API key came back in the response body")
	require.Containsf(t, body, "body.apiKey",
		"the refusal does not name the field that was rejected: %s", body)
}

// TestRefusedAPIKeyLengthIsNotEchoed covers the other constraint on the
// same field. A key that is merely too long is refused by a different
// branch of the validator, which builds its own error detail.
func TestRefusedAPIKeyLengthIsNotEchoed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	submitted := "sk-ant-" + strings.Repeat("A", 600)

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Echo Probe",
			"apiKey": submitted,
		})
	body := string(raw)

	require.Equalf(t, http.StatusUnprocessableEntity, status, "body=%s", body)
	require.NotContainsf(t, body, submitted,
		"the over-long API key came back in the response body")
	require.Containsf(t, body, "body.apiKey",
		"the refusal does not name the field that was rejected: %s", body)
}

// TestMissingRequiredFieldDoesNotEchoTheKey covers the shape a per-field
// rule misses: the validator reports a missing required property against
// the enclosing object and echoes that object, so a request that omits
// an unrelated field still carried the key back out.
func TestMissingRequiredFieldDoesNotEchoTheKey(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	const submitted = "sk-ant-valid-looking-key-0123456789" //#nosec G101 -- synthetic test fixture, never a real key

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			// "kind" omitted.
			"name":   "Echo Probe",
			"apiKey": submitted,
		})
	body := string(raw)

	require.Equalf(t, http.StatusUnprocessableEntity, status, "body=%s", body)
	require.NotContainsf(t, body, submitted,
		"the request body was echoed back with the API key in it")
	require.Containsf(t, body, "kind",
		"the refusal does not say which property was missing: %s", body)
}

// TestRefusedOrdinaryFieldStillEchoesItsValue is the counterweight to
// the three above. Suppressing every value would satisfy them and would
// leave the caller unable to see what the server actually received.
func TestRefusedOrdinaryFieldStillEchoesItsValue(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	submitted := strings.Repeat("n", 101)                       // over the declared maximum of 100
	const wellFormedKey = "sk-ant-valid-looking-key-0123456789" //#nosec G101 -- synthetic test fixture, never a real key

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":         "anthropic",
			"name":         "Echo Probe",
			"defaultModel": submitted,
			"apiKey":       wellFormedKey,
		})
	body := string(raw)

	require.Equalf(t, http.StatusUnprocessableEntity, status, "body=%s", body)
	require.Containsf(t, body, "body.defaultModel",
		"the refusal does not name the field that was rejected: %s", body)
	require.Containsf(t, body, submitted,
		"the refused value was withheld for an ordinary field: %s", body)
}
