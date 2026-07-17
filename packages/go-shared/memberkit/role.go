package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/eventbus"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/eventlog"
)

// roleRank orders workspace roles for privilege comparison. Mirrors the
// workspace_members.role hierarchy: owner > admin > member > guest.
var roleRank = map[Role]int{
	RoleGuest:  1,
	RoleMember: 2,
	RoleAdmin:  3,
	RoleOwner:  4,
}

// EnsureRoleWithinActor reports whether an actor holding actorRole may
// assign, grant, or promote a member to targetRole. A role may only be
// granted when it does not outrank the actor's own role in the workspace
// hierarchy (owner > admin > member > guest), so an admin can never mint
// an owner — only an owner may grant the owner role.
//
// This is the single guard that prevents privilege escalation across the
// add-member, update-role, and create-invite paths. Returns
// ErrRoleEscalation when targetRole outranks actorRole, or a descriptive
// error when either role is not a recognised value.
func EnsureRoleWithinActor(actorRole, targetRole Role) error {
	if !actorRole.IsValid() {
		return fmt.Errorf("memberkit: invalid actor role %q", actorRole)
	}
	if !targetRole.IsValid() {
		return fmt.Errorf("memberkit: invalid target role %q", targetRole)
	}
	if roleRank[targetRole] > roleRank[actorRole] {
		return ErrRoleEscalation
	}
	return nil
}

// UpdateMemberRoleArgs carries the arguments for UpdateMemberRole.
type UpdateMemberRoleArgs struct {
	WorkspaceID uint32
	UserID      uint32
	NewRole     Role
	// ActorUserID is the admin / owner performing the change. Used
	// for the audit event.
	ActorUserID uint32
}

// UpdateMemberRole changes workspace_members.role and appends the
// audit event. Returns sql.ErrNoRows when the membership does not
// exist.
//
// No downstream row is touched: the current design drops the
// workspace-role → calendar-role mapping (calendars no longer have
// cross-member ACL), so a role change is a single UPDATE.
func UpdateMemberRole(ctx context.Context, tx TX, args UpdateMemberRoleArgs) error {
	if !args.NewRole.IsValid() {
		return fmt.Errorf("memberkit: invalid role %q", args.NewRole)
	}
	if args.WorkspaceID == 0 || args.UserID == 0 {
		return fmt.Errorf("memberkit: WorkspaceID and UserID required")
	}

	// Self-modify guard: an actor may not change their own role. Only
	// enforced when the actor is known (non-zero); a zero actor means
	// a system/automated path with no self-modify risk.
	if args.ActorUserID != 0 && args.ActorUserID == args.UserID {
		return ErrSelfModify
	}

	// Load the current role so the audit event records both sides.
	var oldRole string
	err := tx.QueryRowContext(ctx,
		`SELECT role FROM workspace_members
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE
		 LIMIT 1`,
		args.WorkspaceID, args.UserID).Scan(&oldRole)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("memberkit: load current role: %w", err)
	}

	if oldRole == string(args.NewRole) {
		return nil // no-op, no audit row
	}

	// Last-owner guard: demoting an owner to a non-owner role is only
	// allowed while another owner remains. Promotions and lateral
	// owner→owner changes are already excluded by the no-op check above.
	if oldRole == string(RoleOwner) && args.NewRole != RoleOwner {
		owners, err := countWorkspaceOwners(ctx, tx, args.WorkspaceID)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE workspace_members SET role = ?
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		string(args.NewRole), args.WorkspaceID, args.UserID); err != nil {
		return fmt.Errorf("memberkit: update role: %w", err)
	}

	actor := args.ActorUserID
	if actor == 0 {
		actor = args.UserID
	}
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        string(eventbus.WorkspaceMemberRoleChanged),
		WorkspaceID: args.WorkspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"userId":  args.UserID,
			"oldRole": oldRole,
			"newRole": string(args.NewRole),
		},
	}); err != nil {
		return fmt.Errorf("memberkit: append event: %w", err)
	}
	return nil
}
