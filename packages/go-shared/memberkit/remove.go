package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// RemoveWorkspaceMemberArgs carries the arguments for
// RemoveWorkspaceMember. Kept as a struct for consistency with the
// add side and so future flags (e.g. hard-delete) can be added
// without a breaking signature change.
type RemoveWorkspaceMemberArgs struct {
	WorkspaceID uint32
	UserID      uint32
	// ActorUserID is the actor removing the member (admin/owner).
	// Zero falls back to the user themselves (self-leave). Used for
	// the audit event.
	ActorUserID uint32
}

// RemoveWorkspaceMemberResult reports counts of the soft-disable
// cascade so callers (and tests) can verify nothing was missed.
type RemoveWorkspaceMemberResult struct {
	MemberDisabled         bool
	SubscriptionsDisabled  int64
	PersonalCalsDisabled   int64
	TaskActorsDisabled     int64
	ProjectMembersDisabled int64
	MemberAlreadyDisabled  bool
}

// RemoveWorkspaceMember soft-disables a user's workspace membership
// and every workspace-scoped row they owned. The cascade order is
// deliberately leaf-first so a crash between steps leaves no row
// still pointing at an enabled member.
//
// Returns sql.ErrNoRows when the (workspace, user) row is missing —
// distinguishable from the already-disabled case via
// MemberAlreadyDisabled. Callers that treat "remove unknown member"
// as 404 can check for sql.ErrNoRows; everything else is a real
// database error.
func RemoveWorkspaceMember(ctx context.Context, tx *dbretry.Tx, args RemoveWorkspaceMemberArgs) (RemoveWorkspaceMemberResult, error) {
	if args.WorkspaceID == 0 || args.UserID == 0 {
		return RemoveWorkspaceMemberResult{}, fmt.Errorf("memberkit: WorkspaceID and UserID required")
	}

	var res RemoveWorkspaceMemberResult

	// Self-modify guard: an actor may not remove themselves. Only
	// enforced when the actor is known (non-zero); a zero actor is a
	// system/self-leave path handled by the caller, not a guarded
	// admin action.
	if args.ActorUserID != 0 && args.ActorUserID == args.UserID {
		return res, ErrSelfModify
	}

	// Precondition: the member row must exist. Enabled state is
	// informational, not blocking, because a partial earlier remove
	// may have landed the member row but crashed before the cascade.
	// role is read alongside so the last-owner guard can run.
	var (
		targetRole string
		enabled    bool
	)
	err := tx.QueryRowContext(ctx,
		`SELECT role, enabled FROM workspace_members
		 WHERE workspace_id = ? AND user_id = ?
		 LIMIT 1`,
		args.WorkspaceID, args.UserID).Scan(&targetRole, &enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return res, err
		}
		return res, fmt.Errorf("memberkit: load member: %w", err)
	}
	if !enabled {
		res.MemberAlreadyDisabled = true
		// Still run the cascade — a previous partial remove may have
		// left downstream rows enabled.
	}

	// Last-owner guard: removing an owner is only allowed while another
	// owner remains. Only an enabled owner counts toward the total, so
	// an already-disabled owner row cannot be the "last" one.
	if enabled && targetRole == string(RoleOwner) {
		owners, err := countWorkspaceOwners(ctx, tx, args.WorkspaceID)
		if err != nil {
			return res, err
		}
		if owners <= 1 {
			return res, ErrLastOwner
		}
	}

	// Rank guard: the actor must outrank the member they are removing, so
	// an admin cannot remove an owner even when a second owner makes the
	// last-owner guard above indifferent. Ordered after it so the more
	// specific answer still wins when both apply.
	if err := ensureActorMayActOn(ctx, tx, args.WorkspaceID, args.ActorUserID, Role(targetRole)); err != nil {
		return res, err
	}

	// Step 1: soft-disable subscriptions. Leaf rows, safe first.
	if n, err := execCount(ctx, tx,
		`UPDATE calendar_subscriptions SET enabled = FALSE
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID); err != nil {
		return res, fmt.Errorf("memberkit: disable subscriptions: %w", err)
	} else {
		res.SubscriptionsDisabled = n
	}

	// Step 2: soft-disable personal calendars. Events on the
	// calendar stay enabled — other users may be attendees and an
	// audit trail is more useful than pretending the events never
	// happened.
	if n, err := execCount(ctx, tx,
		`UPDATE calendars SET enabled = FALSE
		 WHERE workspace_id = ? AND kind = 'personal'
		   AND owner_user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID); err != nil {
		return res, fmt.Errorf("memberkit: disable personal calendars: %w", err)
	} else {
		res.PersonalCalsDisabled = n
	}

	// Step 3: soft-disable task_actors rows.
	if n, err := execCount(ctx, tx,
		`UPDATE task_actors SET enabled = FALSE
		 WHERE user_id = ?
		   AND task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)
		   AND enabled = TRUE`,
		args.UserID, args.WorkspaceID); err != nil {
		return res, fmt.Errorf("memberkit: disable task_actors: %w", err)
	} else {
		res.TaskActorsDisabled = n
	}

	// Step 4: soft-disable project_members rows.
	if n, err := execCount(ctx, tx,
		`UPDATE project_members SET enabled = FALSE
		 WHERE user_id = ?
		   AND project_id IN (SELECT id FROM projects WHERE workspace_id = ?)
		   AND enabled = TRUE`,
		args.UserID, args.WorkspaceID); err != nil {
		return res, fmt.Errorf("memberkit: disable project_members: %w", err)
	} else {
		res.ProjectMembersDisabled = n
	}

	// Step 5: soft-disable the member row itself last so the earlier
	// steps can still JOIN to workspace_members if they ever need to.
	if n, err := execCount(ctx, tx,
		`UPDATE workspace_members SET enabled = FALSE
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID); err != nil {
		return res, fmt.Errorf("memberkit: disable member: %w", err)
	} else if n > 0 {
		res.MemberDisabled = true
	}

	// Step 6: append audit event.
	actor := args.ActorUserID
	if actor == 0 {
		actor = args.UserID
	}
	userPub, err := userPublicID(ctx, tx, args.UserID)
	if err != nil {
		return res, err
	}
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        eventbus.WorkspaceMemberRemoved,
		WorkspaceID: args.WorkspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"userId":                 userPub.String(),
			"subscriptionsDisabled":  res.SubscriptionsDisabled,
			"personalCalsDisabled":   res.PersonalCalsDisabled,
			"taskActorsDisabled":     res.TaskActorsDisabled,
			"projectMembersDisabled": res.ProjectMembersDisabled,
		},
	}); err != nil {
		return res, fmt.Errorf("memberkit: append event: %w", err)
	}

	return res, nil
}

// execCount runs an UPDATE and returns its RowsAffected.
func execCount(ctx context.Context, tx TX, q string, args ...any) (int64, error) {
	r, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
