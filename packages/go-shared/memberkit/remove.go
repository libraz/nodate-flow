package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
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
	CalendarsTransferred   int64
	CalendarGrantsDisabled int64
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
// One step is not a disable: a shared calendar the member is the sole
// owner of changes hands first, because retiring that grant would leave
// the calendar with an owner nobody can restore.
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
	n, err := execCount(ctx, tx,
		`UPDATE calendar_subscriptions SET enabled = FALSE
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable subscriptions: %w", err)
	}
	res.SubscriptionsDisabled = n

	// Step 2: hand on the calendars this member is the only owner of.
	// Must run before the grants are retired below — promoting a row and
	// then disabling it in the same transaction leaves the calendar
	// exactly as ownerless as doing nothing would have.
	n, err = transferSoleOwnedCalendars(ctx, tx, args.WorkspaceID, args.UserID)
	if err != nil {
		return res, err
	}
	res.CalendarsTransferred = n

	// Step 3: soft-disable the calendar grants the workspace
	// membership implied. Membership in a workspace is what makes a
	// per-calendar grant meaningful, so the grants end with it.
	//
	// Leaving them enabled is what makes a later re-add restore every
	// calendar the user could once reach, including the ones an
	// administrator revoked by hand while they were still a member. The
	// add path grants the personal calendar back deliberately; nothing
	// there decides that the rest should return.
	//
	// Zero rows is a normal outcome: a member may hold no grant at all.
	//
	// This is one of two writers that retire a grant. The other is the
	// DisableCalendarMember query, which revokes a single (calendar,
	// user) pair when someone is removed from one calendar rather than
	// from the workspace. A change to how a revoked grant is represented
	// has to reach both.
	n, err = execCount(ctx, tx,
		`UPDATE calendar_members SET enabled = FALSE
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable calendar grants: %w", err)
	}
	res.CalendarGrantsDisabled = n

	// Step 4: soft-disable personal calendars. Events on the
	// calendar stay enabled — other users may be attendees and an
	// audit trail is more useful than pretending the events never
	// happened.
	n, err = execCount(ctx, tx,
		`UPDATE calendars SET enabled = FALSE
		 WHERE workspace_id = ? AND kind = 'personal'
		   AND owner_user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable personal calendars: %w", err)
	}
	res.PersonalCalsDisabled = n

	// Step 5: soft-disable task_actors rows.
	n, err = execCount(ctx, tx,
		`UPDATE task_actors SET enabled = FALSE
		 WHERE user_id = ?
		   AND task_id IN (SELECT id FROM tasks WHERE workspace_id = ?)
		   AND enabled = TRUE`,
		args.UserID, args.WorkspaceID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable task_actors: %w", err)
	}
	res.TaskActorsDisabled = n

	// Step 6: soft-disable project_members rows.
	n, err = execCount(ctx, tx,
		`UPDATE project_members SET enabled = FALSE
		 WHERE user_id = ?
		   AND project_id IN (SELECT id FROM projects WHERE workspace_id = ?)
		   AND enabled = TRUE`,
		args.UserID, args.WorkspaceID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable project_members: %w", err)
	}
	res.ProjectMembersDisabled = n

	// Step 7: soft-disable the member row itself last so the earlier
	// steps can still JOIN to workspace_members if they ever need to.
	n, err = execCount(ctx, tx,
		`UPDATE workspace_members SET enabled = FALSE
		 WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE`,
		args.WorkspaceID, args.UserID)
	if err != nil {
		return res, fmt.Errorf("memberkit: disable member: %w", err)
	}
	if n > 0 {
		res.MemberDisabled = true
	}

	// Step 8: append audit event.
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
			"calendarsTransferred":   res.CalendarsTransferred,
			"calendarGrantsDisabled": res.CalendarGrantsDisabled,
			"personalCalsDisabled":   res.PersonalCalsDisabled,
			"taskActorsDisabled":     res.TaskActorsDisabled,
			"projectMembersDisabled": res.ProjectMembersDisabled,
		},
	}); err != nil {
		return res, fmt.Errorf("memberkit: append event: %w", err)
	}

	return res, nil
}

// transferSoleOwnedCalendars moves calendar ownership off a departing
// member and returns how many calendars changed hands.
//
// A calendar whose only owner leaves keeps nobody able to manage its
// membership or delete it, and nothing anywhere grants an owner to a
// calendar that has none — the per-calendar removal endpoint refuses to
// remove a last owner for exactly that reason. Workspace removal cannot
// refuse: offboarding a person has to succeed, and failing it over a
// calendar inverts the priority. So the ownership moves instead, which
// makes "every shared calendar has an owner" hold everywhere rather than
// only at the door that can afford to say no.
//
// Calendars the member owns personally are out of scope. The step that
// disables them follows, and a personal calendar losing its owner is the
// ordinary shape of someone leaving, not an orphan.
//
// Zero is a normal answer: most members are the sole owner of nothing
// shared. So is finding no recipient, which needs a workspace with no
// enabled owner at all — a state the last-owner guard already prevents
// from being created. The cascade continues rather than trapping the
// member inside a workspace that is broken for other reasons.
func transferSoleOwnedCalendars(ctx context.Context, tx TX, wsID, userID uint32) (int64, error) {
	calendarIDs, err := findSoleOwnedCalendars(ctx, tx, wsID, userID)
	if err != nil {
		return 0, err
	}
	if len(calendarIDs) == 0 {
		return 0, nil
	}

	recipient, err := findRemainingWorkspaceOwner(ctx, tx, wsID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}

	for _, calID := range calendarIDs {
		// The recipient may hold no grant on this calendar at all, so the
		// write has to be able to create one. member_color is left to the
		// column default on insert and untouched on update: a colour the
		// recipient already carries on that calendar identifies them to
		// everyone else there, and promotion is not a reason to change it.
		publicID := dbtype.New()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calendar_members
			 (public_id, workspace_id, calendar_id, user_id, role)
			 VALUES (?, ?, ?, ?, 'owner')
			 ON DUPLICATE KEY UPDATE role = 'owner', enabled = TRUE`,
			publicID[:], wsID, calID, recipient); err != nil {
			return 0, fmt.Errorf("memberkit: transfer calendar ownership: %w", err)
		}
	}
	return int64(len(calendarIDs)), nil
}

// findSoleOwnedCalendars returns the calendars in the workspace whose
// only live owner is userID, excluding the ones they own personally.
//
// The kind/owner test is spelled out rather than written as a negated
// conjunction because owner_user_id is nullable: a calendar that belongs
// to no one in particular has NULL there, and NOT (kind = 'personal' AND
// owner_user_id = ?) evaluates to NULL for it — dropping exactly the
// group calendars this exists to protect.
func findSoleOwnedCalendars(ctx context.Context, tx TX, wsID, userID uint32) ([]uint32, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT cm.calendar_id
		 FROM calendar_members cm
		 INNER JOIN calendars c ON c.id = cm.calendar_id
		 WHERE cm.workspace_id = ?
		   AND cm.user_id = ?
		   AND cm.role = 'owner'
		   AND cm.enabled = TRUE
		   AND c.enabled = TRUE
		   AND (c.kind <> 'personal'
		        OR c.owner_user_id IS NULL
		        OR c.owner_user_id <> ?)
		   AND (SELECT COUNT(*) FROM calendar_members o
		        WHERE o.calendar_id = cm.calendar_id
		          AND o.role = 'owner'
		          AND o.enabled = TRUE) = 1
		 ORDER BY cm.calendar_id`,
		wsID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("memberkit: find sole-owned calendars: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("memberkit: scan sole-owned calendar: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memberkit: read sole-owned calendars: %w", err)
	}
	return ids, nil
}

// findRemainingWorkspaceOwner returns the workspace owner who inherits
// what a departing member solely owned, or sql.ErrNoRows when the
// workspace has no other enabled owner.
//
// The recipient is the longest-standing remaining owner: the lowest
// workspace_members.id among enabled owners other than the departing
// user. Ordering by the membership row rather than leaving it to the
// server means a workspace with several owners resolves the same way
// every time, so a test that passes does so for a reason.
func findRemainingWorkspaceOwner(ctx context.Context, tx TX, wsID, excludeUserID uint32) (uint32, error) {
	const q = `SELECT user_id FROM workspace_members
	           WHERE workspace_id = ? AND role = ? AND enabled = TRUE
	             AND user_id <> ?
	           ORDER BY id
	           LIMIT 1`
	var userID uint32
	if err := tx.QueryRowContext(ctx, q, wsID, string(RoleOwner), excludeUserID).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		return 0, fmt.Errorf("memberkit: find remaining owner: %w", err)
	}
	return userID, nil
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
