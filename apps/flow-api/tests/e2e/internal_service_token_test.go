package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// internalProbePath is a well-formed /internal/* request target. Every
// test below is refused by the guard before routing, so the snowflake
// only has to satisfy the path pattern.
const internalProbePath = "/internal/users/by-discord/123456789012345678"

// probeInternal sends the probe request with the supplied Authorization
// header value verbatim — header value included, not just the token — so
// a test can present a scheme spelling or a trailing space that a
// token-only helper would normalize away. An empty header value sends no
// Authorization header at all.
func probeInternal(t *testing.T, baseURL, authHeader string) (status int, body []byte, contentType string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+internalProbePath, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw, resp.Header.Get("Content-Type")
}

// newInternalHarness boots a server whose service token is exactly the
// supplied value, including one that is empty or carries whitespace.
// Tests that need the configuration itself to be wrong cannot go through
// the shared harness, which is always correctly configured.
func newInternalHarness(t *testing.T, serviceToken string) string {
	t.Helper()
	srv, cleanup, err := helpers.NewTestServerWithServiceToken(testDB, serviceToken)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return srv.BaseURL
}

// TestInternalRejectsNearMissServiceToken asserts that a bearer close to
// the configured token is refused with the signature-invalid envelope.
//
// The near misses are the inputs a constant-time comparison exists to
// defeat: a strict prefix of the secret and the secret with a single
// byte changed. A comparison that returned early on the first differing
// byte would let a caller extend a guess one byte at a time, and every
// one of those guesses arrives here looking exactly like this. The
// assertion is on the answer, not on the timing — the comparison itself
// is pinned statically where it is written.
func TestInternalRejectsNearMissServiceToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	baseURL := newInternalHarness(t, serviceTokenFixture)

	oneByteOff := []byte(serviceTokenFixture)
	if oneByteOff[len(oneByteOff)-1] == 'b' {
		oneByteOff[len(oneByteOff)-1] = 'c'
	} else {
		oneByteOff[len(oneByteOff)-1] = 'b'
	}

	cases := []struct {
		name  string
		token string
	}{
		{"strict prefix of the token", serviceTokenFixture[:len(serviceTokenFixture)-1]},
		{"token with one byte changed", string(oneByteOff)},
		{"token with one byte appended", serviceTokenFixture + "a"},
		{"unrelated well-formed bearer", "definitely-not-the-right-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, raw, _ := probeInternal(t, baseURL, "Bearer "+tc.token)
			require.Equalf(t, http.StatusUnauthorized, status,
				"%s must not be admitted: got %d body=%s", tc.name, status, string(raw))
			require.Equal(t, "AUTH.TOKEN.SIGNATURE_INVALID", decodeErrorCode(t, raw),
				"a well-formed bearer that is not the secret answers signature-invalid")
		})
	}
}

// TestInternalClosedWhenServiceTokenUnset asserts that leaving the
// service token unset closes /internal/* to everyone rather than
// dropping the group onto some other guard.
//
// The tenant-JWT case is the load-bearing one. The group is mounted
// behind RequireServiceTokenOnly precisely because the sibling guard,
// RequireSignalsAuth, falls through to the JWT chain for any bearer that
// is not the service token — and with no token configured it is nothing
// but that chain. Swapping the mount would leave a cross-workspace user
// lookup reachable by any signed-in user, and this assertion is what
// notices.
func TestInternalClosedWhenServiceTokenUnset(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	baseURL := newInternalHarness(t, "")
	tenant := helpers.CreateTestTenant(t, baseURL)

	cases := []struct {
		name string
		auth string
	}{
		{"no Authorization header", ""},
		{"valid tenant JWT", "Bearer " + tenant.AccessToken},
		{"arbitrary bearer", "Bearer some-arbitrary-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw, _ := probeInternal(t, baseURL, tc.auth)
			require.Equalf(t, http.StatusUnauthorized, status,
				"%s reached /internal/* on a deployment with no service token: got %d body=%s",
				tc.name, status, string(raw))
		})
	}
}

// TestInternalAnswersIdenticallyWhetherOrNotTokenConfigured asserts that
// the 401 a /internal/* request receives does not depend on whether the
// deployment configured a service token.
//
// A difference there is an oracle: it tells a prober whether the machine
// surface is live on this host, which is the one thing worth knowing
// before spending effort guessing the secret. The comparison is between
// two live servers rather than against a copy of the expected envelope,
// so the test cannot pass by having the same answer written down twice.
func TestInternalAnswersIdenticallyWhetherOrNotTokenConfigured(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	configured := newInternalHarness(t, serviceTokenFixture)
	unconfigured := newInternalHarness(t, "")

	cases := []struct {
		name string
		auth string
	}{
		{"well-formed bearer", "Bearer some-arbitrary-value"},
		{"no Authorization header", ""},
		{"malformed Authorization header", "Basic dXNlcjpwYXNz"},
		{"bearer scheme with an empty token", "Bearer "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotBody, gotType := probeInternal(t, configured, tc.auth)
			wantStatus, wantBody, wantType := probeInternal(t, unconfigured, tc.auth)

			require.Equalf(t, wantStatus, gotStatus,
				"%s: status differs between a configured and an unconfigured deployment", tc.name)
			require.Equalf(t, wantType, gotType,
				"%s: Content-Type differs between a configured and an unconfigured deployment", tc.name)
			require.Equalf(t, string(wantBody), string(gotBody),
				"%s: the 401 body tells the caller whether a service token is configured here", tc.name)
		})
	}
}

// TestInternalAcceptsTokenConfiguredWithSurroundingWhitespace asserts
// that a service token carrying a trailing space still admits the client
// that holds the same secret.
//
// A trailing space is what a .env editor or an unquoted compose YAML
// scalar produces, and the parsed bearer is trimmed, so an untrimmed
// configured value can be matched by no client at all. The failure
// reads as AUTH.TOKEN.SIGNATURE_INVALID — "wrong secret" — and sends the
// operator to rotate a secret that was right, on every caller at once.
// Both spellings the client might hold are checked: the secret as
// generated, and the secret as copied out of the file with its space.
func TestInternalAcceptsTokenConfiguredWithSurroundingWhitespace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	baseURL := newInternalHarness(t, serviceTokenFixture+" ")

	for _, tc := range []struct {
		name string
		auth string
	}{
		{"client sends the secret as generated", "Bearer " + serviceTokenFixture},
		{"client sends the secret as copied, space included", "Bearer " + serviceTokenFixture + " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The snowflake is unbound, so the guard passing shows up as
			// the handler's own 404 rather than a 401 from the guard.
			status, raw, _ := probeInternal(t, baseURL, tc.auth)
			require.Equalf(t, http.StatusNotFound, status,
				"the correct secret was refused because the configured copy carried whitespace: got %d body=%s",
				status, string(raw))
			require.Equal(t, "INTEGRATION.DISCORD.USER_NOT_FOUND", decodeErrorCode(t, raw))
		})
	}
}

// TestSignalsAcceptsTokenConfiguredWithSurroundingWhitespace is the
// same assertion for the other guard. POST /signals and /internal/* read
// the same NF_FLOW_API_SIGNAL_TOKEN, so one deployment typo has to be
// survivable on both or the operator fixes half a symptom: a signals
// caller that works while the internal lookup does not reads as two
// unrelated faults.
func TestSignalsAcceptsTokenConfiguredWithSurroundingWhitespace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	srv, cleanup, err := helpers.NewTestServerWithServiceToken(testDB, serviceTokenFixture+" ")
	require.NoError(t, err)
	t.Cleanup(cleanup)
	tenant := helpers.CreateTestTenant(t, srv.BaseURL)

	status, raw := postSignal(t, srv.BaseURL, serviceTokenFixture, map[string]any{
		"workspaceId": tenant.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	})
	require.Equalf(t, http.StatusOK, status,
		"the correct secret was refused on /signals because the configured copy carried whitespace: got %d body=%s",
		status, string(raw))
}

// TestInternalWhitespaceOnlyTokenDoesNotOpenTheGroup asserts that a
// service token consisting only of whitespace leaves /internal/* closed.
//
// Such a value is indistinguishable from an unset one to the operator
// who typed it, so treating it as configured would open the group behind
// a secret nobody could send: the parsed bearer can never be empty or
// blank. It has to read as "off", not as "on with a blank password".
func TestInternalWhitespaceOnlyTokenDoesNotOpenTheGroup(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	baseURL := newInternalHarness(t, "   ")

	for _, auth := range []string{"", "Bearer    ", "Bearer  x ", "Bearer " + serviceTokenFixture} {
		status, raw, _ := probeInternal(t, baseURL, auth)
		require.Equalf(t, http.StatusUnauthorized, status,
			"a whitespace-only service token must leave the group closed; %q got %d body=%s",
			auth, status, string(raw))
	}
}

// TestInternalAcceptsLowercaseBearerScheme asserts that the auth-scheme
// match is case-insensitive.
//
// RFC 7235 defines the scheme as a case-insensitive token, and the
// callers on this surface are machine clients written elsewhere; a
// client that spells it "bearer" holds the right secret and is told its
// token is missing, which names nothing it can act on.
func TestInternalAcceptsLowercaseBearerScheme(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	baseURL := newInternalHarness(t, serviceTokenFixture)

	for _, scheme := range []string{"bearer", "BEARER", "BeArEr"} {
		status, raw, _ := probeInternal(t, baseURL, scheme+" "+serviceTokenFixture)
		require.Equalf(t, http.StatusNotFound, status,
			"scheme %q holds the right secret and must be admitted: got %d body=%s",
			scheme, status, string(raw))
		require.Equal(t, "INTEGRATION.DISCORD.USER_NOT_FOUND", decodeErrorCode(t, raw))
	}
}
