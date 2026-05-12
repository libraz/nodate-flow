// Suspension-vs-delete independence regression for the new immediate
// destructive delete contract.
//
// Before the contract change, "suspend then purge" was a two-leg flow
// guarded by an explicit precondition (the old WORKSPACE.PURGE.NOT_DISABLED
// code). The contract is now:
//
//   - Suspension (PATCH with enabled=false) is reversible and orthogonal.
//   - Delete (DELETE with {confirm: true}) is single-step and destructive.
//   - Suspension is NOT a precondition for delete; admin delete works on
//     any workspace / user regardless of enabled state.
//
// This file pins three behaviours:
//
//   - SubtestA: admin can delete a suspended workspace in one shot.
//   - SubtestB: admin can delete a suspended user in one shot.
//   - SubtestC: owner self-delete on a suspended workspace records the
//     observed contract. The workspace middleware (RequireWorkspaceMember)
//     filters on enabled=TRUE, so a suspended ws yields 404
//     WS.WORKSPACE.NOT_FOUND BEFORE the delete handler runs. Documented
//     as a comment + an assertion so any future change is loud.
//
// Note: the old code path that asserted "delete must reject when ws is
// already disabled" (WORKSPACE.PURGE.NOT_DISABLED) no longer exists and
// is deliberately not re-implemented here — that scenario is impossible
// under the new contract.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// suspendWorkspaceViaAdmin issues PATCH /admin/workspaces/{wsId} with
// enabled=false using the admin token. Asserts 2xx so the suspension
// path itself does not silently fail under us.
func suspendWorkspaceViaAdmin(t *testing.T, adminToken, wsPublicID string) {
	t.Helper()
	enabled := false
	var out struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPatch,
		fmt.Sprintf("%s/admin/workspaces/%s", testServerURL, wsPublicID),
		adminToken, map[string]any{"enabled": &enabled}, &out)
	require.Truef(t, out.Ok, "PATCH /admin/workspaces enabled=false must return ok=true; got %+v", out)
}

// suspendUserViaAdmin issues PATCH /admin/users/{userId} with
// enabled=false using the admin token.
func suspendUserViaAdmin(t *testing.T, adminToken, userPublicID string) {
	t.Helper()
	enabled := false
	var out struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPatch,
		fmt.Sprintf("%s/admin/users/%s", testServerURL, userPublicID),
		adminToken, map[string]any{"enabled": &enabled}, &out)
	require.Truef(t, out.Ok, "PATCH /admin/users enabled=false must return ok=true; got %+v", out)
}

// adminGetWorkspace fetches the workspace through the admin GET
// endpoint and returns the raw status + body so the caller can
// distinguish "exists" from "gone" without choking on a 404.
func adminGetWorkspace(t *testing.T, adminToken, wsPublicID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/admin/workspaces/%s", testServerURL, wsPublicID),
		adminToken, nil)
}

// adminGetUser fetches the user through the admin GET endpoint.
func adminGetUser(t *testing.T, adminToken, userPublicID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/admin/users/%s", testServerURL, userPublicID),
		adminToken, nil)
}

// TestAdminDeleteAfterSuspendWorkspace exercises the admin force-delete
// against a workspace that has been suspended (enabled=false). The
// contract is: suspension is reversible bookkeeping; delete is
// destructive; the two are orthogonal. Admin delete must succeed in one
// call.
func TestAdminDeleteAfterSuspendWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, target.WorkspacePublicID)

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	// 1. Suspend the workspace.
	suspendWorkspaceViaAdmin(t, admin.AccessToken, target.WorkspacePublicID)

	// 2. Verify suspended via the admin GET.
	status, body := adminGetWorkspace(t, admin.AccessToken, target.WorkspacePublicID)
	require.Equalf(t, http.StatusOK, status,
		"admin GET workspace must surface suspended workspaces; got %d body=%s", status, string(body))
	var ws struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(body, &ws), "decode admin workspace GET body=%s", string(body))
	require.Falsef(t, ws.Enabled,
		"PATCH enabled=false must propagate to admin GET; got enabled=%v body=%s",
		ws.Enabled, string(body))

	// 3. Admin delete must succeed in one shot (no NOT_DISABLED precondition).
	out := adminDeleteWorkspace(t, admin.AccessToken, target.WorkspacePublicID)
	require.Truef(t, out.Deleted,
		"admin delete on a suspended workspace must report deleted=true (suspension is "+
			"NOT a precondition); got %+v", out)

	// 4. Workspace row gone.
	require.Falsef(t, workspaceRowExists(t, testDB, wsID),
		"admin delete on suspended workspace must hard-delete the workspaces row")

	// 5. Admin GET now returns 404 (AdminGetWorkspace → sql.ErrNoRows →
	//    InstanceWorkspaceNotFound).
	status, body = adminGetWorkspace(t, admin.AccessToken, target.WorkspacePublicID)
	require.Equalf(t, http.StatusNotFound, status,
		"admin GET on deleted workspace must return 404; got %d body=%s", status, string(body))
}

// TestAdminDeleteAfterSuspendUser exercises the admin force-delete
// against a user account that has been suspended (enabled=false). Same
// orthogonality contract as the workspace case. The target user is NOT
// the admin actor (admin self-delete is rejected with
// USER.DELETE.SELF_NOT_ALLOWED — covered by TestAdminDeleteUserSelfNotAllowed).
func TestAdminDeleteAfterSuspendUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	target := newTenant(t)
	targetUID := internalUserID(t, testDB, target.UserPublicID)

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	// 1. Suspend the target user.
	suspendUserViaAdmin(t, admin.AccessToken, target.UserPublicID)

	// 2. Verify suspended via the admin GET.
	status, body := adminGetUser(t, admin.AccessToken, target.UserPublicID)
	require.Equalf(t, http.StatusOK, status,
		"admin GET user must surface suspended users; got %d body=%s", status, string(body))
	var u struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(body, &u), "decode admin user GET body=%s", string(body))
	require.Falsef(t, u.Enabled,
		"PATCH enabled=false must propagate to admin GET; got enabled=%v body=%s",
		u.Enabled, string(body))

	// 3. Admin delete must succeed in one shot.
	out := adminDeleteUser(t, admin.AccessToken, target.UserPublicID)
	require.Truef(t, out.Deleted,
		"admin delete on a suspended user must report deleted=true (suspension is "+
			"NOT a precondition); got %+v", out)

	// 4. Users row gone.
	require.Falsef(t, userRowExists(t, testDB, targetUID),
		"admin delete on suspended user must hard-delete the users row")

	// 5. Admin GET now returns 404.
	status, body = adminGetUser(t, admin.AccessToken, target.UserPublicID)
	require.Equalf(t, http.StatusNotFound, status,
		"admin GET on deleted user must return 404; got %d body=%s", status, string(body))
}

// TestOwnerDeleteWorkspaceAfterSuspend pins the observed contract for
// the owner self-service path on a suspended workspace.
//
// Observed behaviour. The owner DELETE /workspaces/{wsId} route is
// wrapped in RequireWorkspaceMember, which resolves the workspace via
//
//	SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE
//
// A suspended workspace fails that filter and the middleware would
// emit 404 WS.WORKSPACE.NOT_FOUND. However, the composite test harness
// (apps/flow-api/tests/helpers/server.go::compositeHandler) treats any
// 404 from the primary (auth-api) handler as "route not found on primary,
// try secondary" and re-dispatches the request to flow-api, which does
// not own this route either and returns a plain-text "404 page not
// found". This is an artefact of the harness, not the production
// router; in production the auth-api JSON 404 is returned directly.
//
// What this test pins:
//
//   - Status code 404 (the only stable cross-harness observation).
//   - Workspace row is intact (suspension did NOT destroy it; admin
//     must use the explicit admin delete to actually remove a suspended
//     workspace, which is covered above).
//
// What this test deliberately does NOT pin:
//
//   - The exact error code in the body, because the composite harness
//     swallows the auth-api JSON envelope on this code path. Re-pinning
//     it would require either changing the harness or duplicating the
//     production routing here, neither of which is justified by the
//     value of this single test.
//
// Implication of the contract for users. From the owner's perspective,
// suspension disappears the workspace from their reachable surface
// entirely (every member-scoped endpoint, including DELETE, returns
// 404). To actually reverse the suspension or destroy a suspended
// workspace, an instance admin has to act — admin delete is covered in
// TestAdminDeleteAfterSuspendWorkspace; admin re-enable is the other
// escape hatch.
//
// This test is here to LOCK IN that observation: if anyone re-routes
// the owner delete around RequireWorkspaceMember in the future (so
// suspension no longer blocks owner self-delete), this test will fail
// loudly and force an explicit decision.
func TestOwnerDeleteWorkspaceAfterSuspend(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, owner.WorkspacePublicID)

	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	// Admin suspends the workspace.
	suspendWorkspaceViaAdmin(t, admin.AccessToken, owner.WorkspacePublicID)

	// Owner tries to self-delete. The workspace middleware fires first
	// (RequireWorkspaceMember filters enabled = TRUE) and rejects with
	// 404. See doc comment above for why we do not assert the body code
	// in this harness.
	status, body := doJSONStatus(t, http.MethodDelete,
		fmt.Sprintf("%s/workspaces/%s", testServerURL, owner.WorkspacePublicID),
		owner.AccessToken, map[string]any{"confirm": true})

	require.Equalf(t, http.StatusNotFound, status,
		"owner self-delete on a suspended workspace must be blocked with 404 "+
			"(the RequireWorkspaceMember middleware filters enabled=TRUE so the route "+
			"appears not-found to a non-admin caller); got %d body=%s. If this fails "+
			"with 200, suspension is no longer hiding the workspace from the owner — "+
			"re-evaluate whether the owner should be allowed to delete a workspace "+
			"they were locked out of by an admin.",
		status, string(body))

	// The workspace row is intact — suspension did not destroy it.
	require.Truef(t, workspaceRowExists(t, testDB, wsID),
		"suspension must NOT delete the workspaces row; the workspace must still exist "+
			"and be reachable by an admin")
}
