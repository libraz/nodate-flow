package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// The snowflake lookup joins through four soft-disable flags:
// user_integrations.enabled, users.enabled, workspace_members.enabled and
// workspaces.enabled. Only the first is reachable by varying the fixture
// the suite already inserts, so the other three could be deleted from the
// statement with every test still green — a disabled account would keep
// resolving to a workspace, and a workspace switched off would keep
// receiving signals. The tests below fix each of the remaining three,
// and they disagree about the expected answer on purpose: a disabled
// user has no workspace at all, while a disabled workspace or membership
// leaves the user's other workspaces intact and the lookup has to move
// on to one of them rather than give up.

// membershipWhere addresses one workspace_members row by the public ids
// of the workspace and the user it joins, since neither is exposed as an
// internal id.
func membershipWhere() string {
	return `workspace_id = (SELECT id FROM workspaces WHERE public_id = ?)
	  AND user_id = (SELECT id FROM users WHERE public_id = ?)`
}

// disableRow switches a row's enabled flag off for the duration of the
// test and turns it back on afterwards, so a fixture that fails midway
// cannot leave a disabled account behind for a sibling test. The enable
// and the disable are built from the same clause, so they cannot drift
// into addressing different rows.
func disableRow(t *testing.T, table, where string, args ...any) {
	t.Helper()
	stmt := "UPDATE " + table + " SET enabled = ? WHERE " + where
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := testDB.ExecContext(ctx, stmt, append([]any{false}, args...)...) //#nosec G202 -- table and clause are literals in this file
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(stmt, append([]any{true}, args...)...) //#nosec G202 -- table and clause are literals in this file
	})
}

// addWorkspaceForUser creates a second workspace and joins the user to
// it. The membership is written now, so it sorts after the one the
// tenant registration created and is never the lookup's first choice
// while that earlier one still counts.
func addWorkspaceForUser(t *testing.T, userPublicID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userInternalID := lookupUserInternalID(ctx, t, testDB, userPublicID)

	wsPubID, err := uuid.NewV7()
	require.NoError(t, err)
	wsBin, err := wsPubID.MarshalBinary()
	require.NoError(t, err)
	res, err := testDB.ExecContext(ctx, `
		INSERT INTO workspaces (public_id, slug, name)
		VALUES (?, ?, ?)
	`, wsBin, "later-"+randomHex(6), "Later workspace")
	require.NoError(t, err)
	wsID, err := res.LastInsertId()
	require.NoError(t, err)

	memberPubID, err := uuid.NewV7()
	require.NoError(t, err)
	memberBin, err := memberPubID.MarshalBinary()
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
		VALUES (?, ?, ?, 'owner')
	`, memberBin, wsID, userInternalID)
	require.NoError(t, err)

	// Only the tenant's first workspace is registered for purge, so this
	// one carries its own teardown.
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM workspace_members WHERE workspace_id = ?`, wsID)
		_, _ = testDB.Exec(`DELETE FROM workspaces WHERE id = ?`, wsID)
	})
	return wsPubID.String()
}

// resolvedWorkspace sends the lookup with the service token and returns
// the workspace id it resolved to.
func resolvedWorkspace(t *testing.T, baseURL, snowflake string) string {
	t.Helper()
	status, raw := getByDiscord(t, baseURL, serviceTokenFixture, snowflake)
	require.Equalf(t, http.StatusOK, status, "expected 200, got %d body=%s", status, string(raw))
	var out struct {
		WorkspaceID string `json:"workspaceId"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out.WorkspaceID
}

// TestByDiscordReturns404OnDisabledUser asserts that a disabled account
// resolves to nothing, even though its Discord binding is still enabled.
//
// Disabling a user is how an account is taken out of service; if the
// lookup ignored it, the gateway would keep emitting signals in that
// user's name from a Discord session nobody revoked.
func TestByDiscordReturns404OnDisabledUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)

	userPub, err := types.Parse(h.tenant.UserPublicID)
	require.NoError(t, err)
	disableRow(t, "users", "public_id = ?", userPub)

	status, raw := getByDiscord(t, h.baseURL, serviceTokenFixture, snowflake)
	require.Equalf(t, http.StatusNotFound, status,
		"a disabled account still resolved: got %d body=%s", status, string(raw))
	require.Equal(t, "INTEGRATION.DISCORD.USER_NOT_FOUND", decodeErrorCode(t, raw))
}

// TestByDiscordSkipsDisabledWorkspace asserts that a disabled workspace
// is passed over rather than treated as the answer or as a dead end.
//
// The user is still a member of somewhere they can work, so the lookup
// must move to the next enabled workspace. Answering 404 here would take
// the user's signals away along with the workspace that was switched
// off, and answering with the disabled workspace would file them under
// it.
func TestByDiscordSkipsDisabledWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)
	laterWorkspace := addWorkspaceForUser(t, h.tenant.UserPublicID)

	require.Equal(t, h.tenant.WorkspacePublicID, resolvedWorkspace(t, h.baseURL, snowflake),
		"the earliest workspace should win while it is enabled")

	wsPub, err := types.Parse(h.tenant.WorkspacePublicID)
	require.NoError(t, err)
	disableRow(t, "workspaces", "public_id = ?", wsPub)

	require.Equal(t, laterWorkspace, resolvedWorkspace(t, h.baseURL, snowflake),
		"the disabled workspace was still chosen, or the lookup gave up instead of moving to the next enabled one")
}

// TestByDiscordSkipsDisabledMembership asserts the same for a membership
// that was switched off while the workspace itself stays enabled.
//
// Removing someone from a workspace must stop their signals landing
// there, and it must not stop them landing anywhere.
func TestByDiscordSkipsDisabledMembership(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)
	laterWorkspace := addWorkspaceForUser(t, h.tenant.UserPublicID)

	require.Equal(t, h.tenant.WorkspacePublicID, resolvedWorkspace(t, h.baseURL, snowflake),
		"the earliest membership should win while it is enabled")

	wsPub, err := types.Parse(h.tenant.WorkspacePublicID)
	require.NoError(t, err)
	userPub, err := types.Parse(h.tenant.UserPublicID)
	require.NoError(t, err)
	disableRow(t, "workspace_members", membershipWhere(), wsPub, userPub)

	require.Equal(t, laterWorkspace, resolvedWorkspace(t, h.baseURL, snowflake),
		"the disabled membership was still chosen, or the lookup gave up instead of moving to the next enabled one")
}
