package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/memberkit"
)

// TestRoleEscalationMapsToRoleDenied pins the wire contract shared by the
// add-member, update-role, and create-invite handlers: when the shared
// memberkit guard rejects a grant that outranks the actor, the handler
// surfaces WS.MEMBER.ROLE_DENIED (403). This is the code all three
// handlers pass to httpErr on memberkit.ErrRoleEscalation.
func TestRoleEscalationMapsToRoleDenied(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "WS.MEMBER.ROLE_DENIED", apierrors.WsMemberRoleDenied.Code)
	assert.Equal(t, 403, apierrors.WsMemberRoleDenied.Status,
		"a privilege-escalation attempt must be forbidden (403), never silently granted")
}

// TestAdminCannotGrantOwner_OwnerCan exercises the exact escalation matrix
// the handlers apply: they call memberkit.EnsureRoleWithinActor with the
// actor's workspace role (middleware.WorkspaceRole) converted to a
// memberkit.Role. An admin actor granting owner is rejected; an owner
// actor granting owner succeeds.
func TestAdminCannotGrantOwner_OwnerCan(t *testing.T) {
	t.Parallel()

	// Admin promoting/inviting to owner -> escalation (handler returns 403).
	adminActor := memberkit.Role(middleware.WorkspaceRoleAdmin)
	require.ErrorIs(t,
		memberkit.EnsureRoleWithinActor(adminActor, memberkit.RoleOwner),
		memberkit.ErrRoleEscalation,
		"an admin must not be able to grant owner")

	// Owner doing the same -> allowed.
	ownerActor := memberkit.Role(middleware.WorkspaceRoleOwner)
	assert.NoError(t,
		memberkit.EnsureRoleWithinActor(ownerActor, memberkit.RoleOwner),
		"an owner may grant owner")

	// Admin granting a role within reach is still fine.
	assert.NoError(t,
		memberkit.EnsureRoleWithinActor(adminActor, memberkit.RoleMember),
		"an admin may grant member")
}
