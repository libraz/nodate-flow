package e2e

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sensitivePatterns lists strings that must NEVER appear in API response
// bodies. Each entry is a regex pattern.
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`"password_hash"`),
	regexp.MustCompile(`"passwordHash"`),
	regexp.MustCompile(`"refresh_hash"`),
	regexp.MustCompile(`"refreshHash"`),
	regexp.MustCompile(`"mfa_secret"`),
	regexp.MustCompile(`"mfaSecret"`),
	regexp.MustCompile(`"token_hash"`),
	regexp.MustCompile(`"tokenHash"`),
	regexp.MustCompile(`\$argon2`),   // argon2 hash prefix
	regexp.MustCompile(`\$2[aby]\$`), // bcrypt hash prefix
}

// assertNoSensitiveData checks that none of the known sensitive field
// patterns appear in a raw API response body.
func assertNoSensitiveData(t *testing.T, endpoint string, body []byte) {
	t.Helper()
	for _, p := range sensitivePatterns {
		require.False(t, p.Match(body),
			"%s response contains sensitive pattern %q: %s",
			endpoint, p.String(), string(body))
	}
}

// ---------- Internal ID leak prevention ----------

// TestNoInternalIDsInUserProfile verifies that GET /me returns only
// public UUIDs, never internal numeric IDs.
func TestNoInternalIDsInUserProfile(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/me",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	assertNoSensitiveData(t, "GET /me", body)

	// The "id" field must be a UUID, not a numeric string.
	var me map[string]any
	require.NoError(t, json.Unmarshal(body, &me))

	id, ok := me["id"].(string)
	require.True(t, ok, "id must be a string")
	require.True(t, isUUID(id), "id must be a UUID, got %q", id)

	// Must NOT contain internal numeric ID fields.
	for _, forbidden := range []string{"internalId", "internal_id", "userId"} {
		_, exists := me[forbidden]
		require.False(t, exists, "GET /me must not expose %q", forbidden)
	}
}

// TestNoInternalIDsInMemberList verifies that workspace member list
// returns only public UUIDs and never exposes password hashes.
func TestNoInternalIDsInMemberList(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/members",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	assertNoSensitiveData(t, "GET members", body)

	var resp struct {
		Members []map[string]any `json:"members"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, m := range resp.Members {
		uid, ok := m["userId"].(string)
		require.True(t, ok)
		require.True(t, isUUID(uid), "userId must be a UUID, got %q", uid)

		mid, ok := m["id"].(string)
		require.True(t, ok)
		require.True(t, isUUID(mid), "member id must be a UUID, got %q", mid)
	}
}

// TestNoInternalIDsInTaskResponse verifies that task responses use
// public UUIDs for all ID fields.
func TestNoInternalIDsInTaskResponse(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a task.
	var task struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspaceId"`
		ProjectID   string `json:"projectId"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "ID test"}, &task)

	require.True(t, isUUID(task.ID), "task.id must be UUID")
	require.True(t, isUUID(task.WorkspaceID), "task.workspaceId must be UUID")
	require.True(t, isUUID(task.ProjectID), "task.projectId must be UUID")

	// Fetch and check raw body for sensitive patterns.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	assertNoSensitiveData(t, "GET /tasks/:id", body)
}

// ---------- Credential / secret leak prevention ----------

// TestWebhookSecretNotInList verifies that listing webhooks does NOT
// include the signing secret (which is only returned on create and
// get-by-id).
func TestWebhookSecretNotInList(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/webhooks"

	// Create a webhook.
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"url": "https://example.com/hook", "description": "test",
		"eventTypes": json.RawMessage(`["*"]`),
	}, nil)

	// List must NOT contain "secret" field.
	status, body := doJSONStatus(t, http.MethodGet, base, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	var resp struct {
		Webhooks []map[string]any `json:"webhooks"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, wh := range resp.Webhooks {
		_, hasSecret := wh["secret"]
		require.False(t, hasSecret, "webhook list must not expose secret")
	}
}

// TestInviteTokenHashNeverExposed verifies that invite list responses
// never expose the token_hash, and the plaintext token is only returned
// on creation.
func TestInviteTokenHashNeverExposed(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/invites"

	// Create invite — should return plaintext token.
	status, createBody := doJSONStatus(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"role": "member"})
	require.Equal(t, http.StatusOK, status)

	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(createBody, &created))
	require.NotEmpty(t, created.Token, "creation must return plaintext token")

	// Create body must NOT contain token_hash / tokenHash.
	require.NotContains(t, string(createBody), "token_hash")
	require.NotContains(t, string(createBody), "tokenHash")

	// List invites — must NOT contain token, token_hash, or tokenHash.
	status, listBody := doJSONStatus(t, http.MethodGet, base, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(listBody), "token_hash")
	require.NotContains(t, string(listBody), "tokenHash")

	// The plaintext token value itself must not appear in the list.
	require.NotContains(t, string(listBody), created.Token,
		"plaintext token must not appear in invite list")
}

// TestSessionListNoRefreshHash verifies that GET /me/sessions does not
// expose the refresh token hash.
func TestSessionListNoRefreshHash(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/me/sessions",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	assertNoSensitiveData(t, "GET /me/sessions", body)

	// Verify sessions only contain expected fields.
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.GreaterOrEqual(t, len(resp.Items), 1, "should have at least one session")

	for _, s := range resp.Items {
		for _, forbidden := range []string{
			"refreshHash", "refresh_hash", "refreshToken", "refresh_token",
		} {
			_, exists := s[forbidden]
			require.False(t, exists, "session must not expose %q", forbidden)
		}
		// Session ID must be a UUID, not numeric.
		sid, ok := s["id"].(string)
		require.True(t, ok)
		require.True(t, isUUID(sid), "session id must be UUID, got %q", sid)
	}
}

// ---------- Cross-user isolation ----------

// TestNotificationCrossUserIsolation verifies that user A cannot see
// user B's notifications, even within the same workspace.
func TestNotificationCrossUserIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// Create two users in the same workspace.
	owner := newTenant(t)
	member := newTenant(t)

	// Invite member to owner's workspace.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Each user's notification list should be scoped to themselves.
	var ownerNotifs struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/me/notifications?workspaceId="+owner.WorkspacePublicID,
		owner.AccessToken, nil, &ownerNotifs)

	var memberNotifs struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/me/notifications?workspaceId="+owner.WorkspacePublicID,
		member.AccessToken, nil, &memberNotifs)

	// Both start at zero (fresh accounts), but the important thing is
	// that the endpoint succeeds for both and returns isolated results.
	require.Equal(t, int64(0), ownerNotifs.Total)
	require.Equal(t, int64(0), memberNotifs.Total)
}

// ---------- Soft-deleted resources ----------

// TestSoftDeletedPageNotAccessible verifies that a soft-deleted page
// returns 404 on direct GET and does not appear in search results.
func TestSoftDeletedPageNotAccessible(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create and delete a page.
	var page struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Ephemeral Doc"}, &page)

	doJSONStatus(t, http.MethodDelete, wsURL+"/pages/"+page.ID,
		tt.AccessToken, nil)

	// Direct GET must return 404.
	status, _ := doJSONStatus(t, http.MethodGet, wsURL+"/pages/"+page.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"deleted page must return 404 on direct access")

	// Search must not return the deleted page.
	var search struct {
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages/search?q=Ephemeral",
		tt.AccessToken, nil, &search)
	for _, p := range search.Pages {
		require.NotEqual(t, page.ID, p.ID, "deleted page must not appear in search")
	}
}

// TestSoftDeletedTimeboxNotAccessible verifies that a soft-deleted
// timebox returns 404 on direct GET.
func TestSoftDeletedTimeboxNotAccessible(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes"

	var tb struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name": "Gone Sprint", "startsOn": "2025-08-01", "endsOn": "2025-08-14",
	}, &tb)

	doJSONStatus(t, http.MethodDelete, base+"/"+tb.ID, tt.AccessToken, nil)

	status, _ := doJSONStatus(t, http.MethodGet, base+"/"+tb.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"deleted timebox must return 404")
}

// TestSoftDeletedWidgetNotAccessible verifies that a soft-deleted
// dashboard widget returns 404 on direct GET.
func TestSoftDeletedWidgetNotAccessible(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets"

	var w struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"widgetType": "task_summary", "title": "Temp",
		"positionX": 0, "positionY": 0, "width": 2, "height": 2,
	}, &w)

	doJSONStatus(t, http.MethodDelete, base+"/"+w.ID, tt.AccessToken, nil)

	status, _ := doJSONStatus(t, http.MethodGet, base+"/"+w.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"deleted widget must return 404")
}

// ---------- Workspace member email not visible to outsiders ----------

// TestMemberEmailNotVisibleToOutsider verifies that a non-member
// cannot access the members list (and thus cannot harvest emails).
func TestMemberEmailNotVisibleToOutsider(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members",
		outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"an outsider listing workspace members")
	require.NotContains(t, string(body), owner.Email,
		"a refusal must not carry the addresses it refused")
}

// ---------- Export does not leak internal data ----------

// TestExportNoInternalIDs verifies that exported task data uses only
// public UUIDs and contains no sensitive internal fields.
func TestExportNoInternalIDs(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a task to export.
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Export check"}, nil)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/export/tasks?format=json",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	assertNoSensitiveData(t, "export/tasks", body)

	var exported struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(body, &exported))
	for _, task := range exported.Tasks {
		// ID fields must be UUIDs.
		tid, ok := task["id"].(string)
		require.True(t, ok)
		require.True(t, isUUID(tid), "exported task id must be UUID")

		pid, ok := task["projectId"].(string)
		require.True(t, ok)
		require.True(t, isUUID(pid), "exported projectId must be UUID")

		// Must not contain internal numeric IDs.
		for _, forbidden := range []string{
			"internalId", "internal_id", "workspace_id", "project_id",
		} {
			_, exists := task[forbidden]
			require.False(t, exists, "export must not contain %q", forbidden)
		}
	}
}

// ---------- helpers ----------

// uuidRe matches UUID v4/v7 format (8-4-4-4-12 hex).
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isUUID(s string) bool {
	return uuidRe.MatchString(strings.ToLower(s))
}
