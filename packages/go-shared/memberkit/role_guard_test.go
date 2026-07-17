package memberkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureRoleWithinActor_BlocksEscalation pins the privilege-escalation
// guard shared by the add-member, update-role, and create-invite handlers:
// an actor may only assign a role at or below their own in the hierarchy
// (owner > admin > member > guest). Crucially, an admin granting owner is
// rejected while an owner granting owner succeeds.
func TestEnsureRoleWithinActor_BlocksEscalation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		actor     Role
		target    Role
		wantEscal bool
	}{
		// The regression this guard closes: an admin must not be able to
		// mint or promote to owner.
		{"admin cannot grant owner", RoleAdmin, RoleOwner, true},
		{"member cannot grant owner", RoleMember, RoleOwner, true},
		{"member cannot grant admin", RoleMember, RoleAdmin, true},
		{"guest cannot grant member", RoleGuest, RoleMember, true},

		// Only an owner may grant owner.
		{"owner can grant owner", RoleOwner, RoleOwner, false},
		{"owner can grant admin", RoleOwner, RoleAdmin, false},
		{"owner can grant member", RoleOwner, RoleMember, false},
		{"owner can grant guest", RoleOwner, RoleGuest, false},

		// Lateral and downward grants within an admin's reach are allowed.
		{"admin can grant admin", RoleAdmin, RoleAdmin, false},
		{"admin can grant member", RoleAdmin, RoleMember, false},
		{"admin can grant guest", RoleAdmin, RoleGuest, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := EnsureRoleWithinActor(tc.actor, tc.target)
			if tc.wantEscal {
				require.ErrorIs(t, err, ErrRoleEscalation,
					"actor=%s target=%s must be rejected as escalation", tc.actor, tc.target)
			} else {
				assert.NoError(t, err,
					"actor=%s target=%s is at or below the actor's role", tc.actor, tc.target)
			}
		})
	}
}

// TestEnsureRoleWithinActor_RejectsUnknownRoles guards against a caller
// passing an unrecognised role string (e.g. from an out-of-sync enum). An
// unknown role must never silently pass the escalation check.
func TestEnsureRoleWithinActor_RejectsUnknownRoles(t *testing.T) {
	t.Parallel()

	err := EnsureRoleWithinActor(Role("superuser"), RoleOwner)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRoleEscalation,
		"an invalid actor role is a validation error, not an escalation")

	err = EnsureRoleWithinActor(RoleOwner, Role("superuser"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRoleEscalation,
		"an invalid target role is a validation error, not an escalation")
}
