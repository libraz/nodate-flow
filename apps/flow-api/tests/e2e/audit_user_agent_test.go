package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/packages/go-shared/httputil"
	"github.com/stretchr/testify/require"
)

// TestAuditStoresAUserAgentCutOnARuneBoundary drives a request whose
// User-Agent is longer than the stored cap and whose multi-byte rune
// straddles it.
//
// net/http accepts obs-text in a header value, so a User-Agent is not
// ASCII by contract and any client can send this one. The cap has to land
// on a rune boundary: a cut on the raw byte index leaves a fragment that
// is not valid UTF-8, the utf8mb4 column refuses it under
// STRICT_TRANS_TABLES, and the recorder turns that refusal into a warning
// — so the audit entry for the request never exists. This test therefore
// asserts the row is there before it asserts anything about its contents.
func TestAuditStoresAUserAgentCutOnARuneBoundary(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// Two bytes short of the cap, then a three-byte rune: the cap falls
	// inside that rune, which is the case a byte cut severs.
	const head = "a"
	prefix := strings.Repeat(head, httputil.UserAgentMaxLen-2)
	userAgent := prefix + "日本語"
	require.Greater(t, len(userAgent), httputil.UserAgentMaxLen,
		"the crafted User-Agent has to exceed the cap or nothing is truncated")
	require.False(t, utf8.RuneStart(userAgent[httputil.UserAgentMaxLen]),
		"the crafted User-Agent has to straddle the cap or a byte cut would survive it")

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "User-Agent host",
	}, &task)
	require.NotEmpty(t, task.ID)

	// The comment write is the audited action; it carries the crafted
	// header so the recorder stores that User-Agent.
	var comment struct {
		ID string `json:"id"`
	}
	doJSONWithUserAgent(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, userAgent, map[string]any{"body": "audited under a multi-byte user agent"}, &comment)
	require.NotEmpty(t, comment.ID)

	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?limit=50&offset=0", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)

	var entry *auditListEntry
	for i := range resp.Entries {
		if resp.Entries[i].Action == "comment.create" {
			entry = &resp.Entries[i]
			break
		}
	}
	require.NotNil(t, entry,
		"no comment.create audit row exists for a request the API answered 2xx; the "+
			"recorder's write was refused and the failure was logged instead of surfaced")
	require.NotNil(t, entry.UserAgent, "the audit row must carry the caller's User-Agent")

	stored := *entry.UserAgent
	require.True(t, utf8.ValidString(stored), "stored User-Agent is not valid UTF-8: %q", stored)
	require.False(t, strings.ContainsRune(stored, utf8.RuneError),
		"stored User-Agent carries U+FFFD: %q", stored)
	require.LessOrEqual(t, len(stored), httputil.UserAgentMaxLen,
		"stored User-Agent is over the cap")
	require.Equal(t, prefix, stored,
		"the cap has to fall back to the last rune boundary, keeping the whole ASCII run "+
			"and dropping the rune it would otherwise have cut in half")
}

// doJSONWithUserAgent sends a JSON request under an explicit User-Agent
// and asserts a 2xx status. The shared helpers leave the header to the
// http package, which is what every other test wants.
func doJSONWithUserAgent(t *testing.T, method, url, bearer, userAgent string, body any, out any) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.GreaterOrEqualf(t, resp.StatusCode, 200, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))
	require.Lessf(t, resp.StatusCode, 300, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), "decode %s %s body=%s", method, url, string(raw))
	}
}
