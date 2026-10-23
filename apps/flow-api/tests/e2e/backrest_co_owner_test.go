package e2e

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// addCoOwner makes a fresh user a second owner of tt's workspace.
//
// Deleting a user who is the only owner of a workspace is refused with
// WS.MEMBER.LAST_OWNER, so a test whose subject is what deleting the user
// removes gives the tenant's workspace another owner first.
func addCoOwner(t *testing.T, tt *helpers.TestTenant) {
	t.Helper()
	ctx := context.Background()
	other := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)
	uid := lookupUserInternalID(ctx, t, testDB, other.UserPublicID)
	pub := uuid.Must(uuid.NewV7())
	_, err := helpers.ExecRetry(ctx, testDB, "test seed: co-owner",
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		pub[:], wsID, uid)
	require.NoError(t, err)
}
