package e2e

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedInvocation writes one ai_invocations row whose prompt is marker,
// optionally attributed to a task and a user.
func seedInvocation(t *testing.T, wsID, providerID uint32, taskID, userID sql.NullInt32, marker string) {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	_, err := testDB.Exec(`
		INSERT INTO ai_invocations (public_id, workspace_id, provider_id, user_id, task_id, purpose, model, prompt_redacted, status, invoked_at)
		VALUES (?, ?, ?, ?, ?, 'propose_steps', 'test-model', ?, 'ok', ?)`,
		pub[:], wsID, providerID, userID, taskID, marker, time.Now().UTC())
	require.NoError(t, err)
}

// TestAiInvocationsListHidesPromptsAboutUnreadableTasks holds the
// workspace invocation list to task visibility. Redaction strips secrets,
// not task content, so a prompt built from a private task carries that
// task's words; a guest must not read it, nor a task-less prompt another
// user made, while a prompt about a task they can read stays listed.
func TestAiInvocationsListHidesPromptsAboutUnreadableTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	privateTask := createTaskWithVisibility(t, owner, "Vis AI private", "private")
	publicTask := createTaskWithVisibility(t, owner, "Vis AI public", "public")
	wsID, privateTaskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, privateTask)
	_, publicTaskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, publicTask)

	var ownerUserID int32
	require.NoError(t, testDB.QueryRow(
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, owner.UserPublicID,
	).Scan(&ownerUserID))

	provPub := uuid.Must(uuid.NewV7())
	res, err := testDB.Exec(`
		INSERT INTO ai_providers (public_id, workspace_id, kind, name, api_key_ciphertext, api_key_prefix, api_key_suffix, default_model)
		VALUES (?, ?, 'openai_compat', 'vis-prov', X'00', 'sk-test1', 'last', 'test-model')`,
		provPub[:], wsID)
	require.NoError(t, err)
	provID64, err := res.LastInsertId()
	require.NoError(t, err)
	providerID := uint32(provID64) //nolint:gosec // test-scoped LastInsertId fits uint32

	suffix := uuid.NewString()
	privateMarker := "vis-ai private prompt " + suffix
	ownCallMarker := "vis-ai owner task-less prompt " + suffix
	publicMarker := "vis-ai public prompt " + suffix

	seedInvocation(t, wsID, providerID, sql.NullInt32{Int32: int32(privateTaskID), Valid: true}, sql.NullInt32{}, privateMarker) //nolint:gosec // test-scoped id fits int32
	seedInvocation(t, wsID, providerID, sql.NullInt32{}, sql.NullInt32{Int32: ownerUserID, Valid: true}, ownCallMarker)
	seedInvocation(t, wsID, providerID, sql.NullInt32{Int32: int32(publicTaskID), Valid: true}, sql.NullInt32{}, publicMarker) //nolint:gosec // test-scoped id fits int32

	list := func(token string) string {
		status, body := doJSONStatus(t, http.MethodGet,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/ai/invocations?limit=200",
			token, nil)
		require.Equal(t, http.StatusOK, status, "invocation list must render; body=%s", string(body))
		return string(body)
	}

	got := list(guest.AccessToken)
	assert.NotContains(t, got, privateMarker, "a prompt about a private task must not reach a guest")
	assert.NotContains(t, got, ownCallMarker, "another user's task-less prompt must not reach a guest")
	assert.Contains(t, got, publicMarker, "a prompt about a readable task must still be listed")

	ownerBody := list(owner.AccessToken)
	for _, m := range []string{privateMarker, ownCallMarker, publicMarker} {
		assert.Contains(t, ownerBody, m, "the workspace owner reads every invocation")
	}
}
