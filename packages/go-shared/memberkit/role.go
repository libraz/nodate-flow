package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
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
// This is the grant half of the escalation guard; [EnsureActorOutranksTarget]
// is the other half. Returns ErrRoleEscalation when targetRole outranks
// actorRole, or a descriptive error when either role is not a recognised
// value.
func EnsureRoleWithinActor(actorRole, targetRole Role) error {
	return ensureRankWithin(actorRole, targetRole)
}

// EnsureActorOutranksTarget reports whether an actor holding actorRole
// may modify a member who currently holds targetRole.
//
// Guarding only the role being granted leaves the mirror image open: an
// admin who cannot mint an owner can still demote one to member and then
// remove them, which costs the workspace an owner and hands the
// owner-only delete gate to whoever is left. The current role of the
// person being acted on therefore gates the action just as the new role
// does, and both directions have to hold — closing one and leaving the
// other is how this came back after the grant side was fixed.
//
// Equal ranks pass: one owner may act on another, mirroring the calendar
// member rules where only an owner may touch an owner.
func EnsureActorOutranksTarget(actorRole, targetRole Role) error {
	return ensureRankWithin(actorRole, targetRole)
}

// ensureRankWithin is the single comparison behind both guards: the
// other role may not outrank the actor's.
func ensureRankWithin(actorRole, otherRole Role) error {
	if !actorRole.IsValid() {
		return fmt.Errorf("memberkit: invalid actor role %q", actorRole)
	}
	if !otherRole.IsValid() {
		return fmt.Errorf("memberkit: invalid target role %q", otherRole)
	}
	if roleRank[otherRole] > roleRank[actorRole] {
		return ErrRoleEscalation
	}
	return nil
}

// loadMemberRole reads a member's current workspace role. Returns
// sql.ErrNoRows when the user is not a member.
func loadMemberRole(ctx context.Context, tx TX, workspaceID, userID uint32) (Role, error) {
	var role string
	if err := tx.QueryRowContext(ctx,
		`SELECT role FROM workspace_members
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE
		 LIMIT 1`,
		workspaceID, userID).Scan(&role); err != nil {
		return "", err
	}
	return Role(role), nil
}

// ensureActorMayActOn loads the actor's own role and checks it against
// the target's current role.
//
// The lookup happens here rather than in the caller so no write path can
// reach the member tables without the check: the handlers used to be the
// only place this was enforced, and the one that forgot is exactly the
// hole this closes. A zero actor is a system path with no privilege to
// escalate and is left alone; an actor with no membership row cannot
// outrank anybody, so it is refused rather than skipped.
func ensureActorMayActOn(ctx context.Context, tx TX, workspaceID, actorUserID uint32, targetRole Role) error {
	if actorUserID == 0 {
		return nil
	}
	actorRole, err := loadMemberRole(ctx, tx, workspaceID, actorUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRoleEscalation
		}
		return fmt.Errorf("memberkit: load actor role: %w", err)
	}
	return EnsureActorOutranksTarget(actorRole, targetRole)
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

	// The actor must outrank the member as they stand today, not just the
	// role they are being moved to: without this an admin can demote an
	// owner and then remove them. Ordered after the last-owner guard so
	// the more specific "the workspace would lose its only owner" answer
	// still wins when both apply.
	if err := ensureActorMayActOn(ctx, tx, args.WorkspaceID, args.ActorUserID, Role(oldRole)); err != nil {
		return err
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
	userPub, err := userPublicID(ctx, tx, args.UserID)
	if err != nil {
		return err
	}
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        string(eventbus.WorkspaceMemberRoleChanged),
		WorkspaceID: args.WorkspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"userId":  userPub.String(),
			"oldRole": oldRole,
			"newRole": string(args.NewRole),
		},
	}); err != nil {
		return fmt.Errorf("memberkit: append event: %w", err)
	}
	return nil
}
