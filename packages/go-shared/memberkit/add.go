package memberkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// AddWorkspaceMemberArgs carries every input AddWorkspaceMember needs.
// Callers authorise the actor before invoking; memberkit writes only.
type AddWorkspaceMemberArgs struct {
	// WorkspaceID is the internal FK into workspaces.
	WorkspaceID uint32
	// UserID is the internal FK into users. Callers that invite by
	// email must resolve the email to a users.id (creating a stub
	// user if needed) before calling memberkit.
	UserID uint32
	// Role is the role stored in workspace_members. Must be a value
	// of the Role type.
	Role Role
	// InvitedByUserID is the inviter's internal id, or zero when the
	// user joined via self-registration (first workspace owner).
	InvitedByUserID uint32
	// JoinedAt is when the join completed. Zero means "now". Passing
	// a past timestamp lets the invite-accept path use the
	// invited_at of the underlying invite.
	JoinedAt time.Time
	// InvitedAt is when the invite was sent. Zero means "same as
	// JoinedAt" (self-registration / direct-add flow).
	InvitedAt time.Time
	// EnsurePersonalCalendar controls whether memberkit materialises
	// a personal calendar + subscription on add. Normally true; set
	// false only for specialised flows (e.g. re-enabling an existing
	// member who already had a calendar).
	EnsurePersonalCalendar bool
}

// AddWorkspaceMemberResult reports which side effects memberkit
// actually performed. Handlers can use this to decide whether to
// surface an informational message ("We created your calendar layer")
// to the user on first join.
type AddWorkspaceMemberResult struct {
	MemberID           uint32
	MemberPublicID     dbtype.PublicID
	CreatedMember      bool
	ReenabledMember    bool
	CreatedCalendar    bool
	PersonalCalendarID uint32
}

// AddWorkspaceMember inserts workspace_members and ensures the new
// member has the personal-calendar layer their calendar UI expects.
// Idempotent on (workspace_id, user_id): re-adding an enabled member
// returns their existing row without touching the calendar side; a
// previously-removed (soft-disabled) member is re-enabled.
//
// Invariant: the three possible side effects (member row, personal
// calendar, personal subscription) all happen inside the caller's
// transaction. If any step fails, the caller rolls back and none of
// the rows stick.
func AddWorkspaceMember(ctx context.Context, tx *dbretry.Tx, args AddWorkspaceMemberArgs) (AddWorkspaceMemberResult, error) {
	if !args.Role.IsValid() {
		return AddWorkspaceMemberResult{}, fmt.Errorf("memberkit: invalid role %q", args.Role)
	}
	if args.WorkspaceID == 0 || args.UserID == 0 {
		return AddWorkspaceMemberResult{}, fmt.Errorf("memberkit: WorkspaceID and UserID required")
	}

	now := args.JoinedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	invited := args.InvitedAt
	if invited.IsZero() {
		invited = now
	}

	var res AddWorkspaceMemberResult

	// Step 1: upsert the member row.
	existingID, existingEnabled, err := findExistingMember(ctx, tx, args.WorkspaceID, args.UserID)
	switch {
	case err == nil:
		// Row exists. Re-enable if previously removed; leave role
		// alone (UpdateMemberRole is the dedicated path).
		res.MemberID = existingID
		if !existingEnabled {
			if _, err := tx.ExecContext(ctx,
				`UPDATE workspace_members
				 SET enabled = TRUE, joined_at = ?
				 WHERE id = ?`,
				sql.NullTime{Time: now, Valid: true}, existingID); err != nil {
				return res, fmt.Errorf("memberkit: re-enable member: %w", err)
			}
			res.ReenabledMember = true
		}
		// Look up public id for the result.
		if err := tx.QueryRowContext(ctx,
			`SELECT public_id FROM workspace_members WHERE id = ?`, existingID).
			Scan(&res.MemberPublicID); err != nil {
			return res, fmt.Errorf("memberkit: load member public id: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		// Fresh insert.
		pubID := dbtype.New()
		var invitedByArg any
		if args.InvitedByUserID != 0 {
			invitedByArg = args.InvitedByUserID
		}
		ins, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members
			 (public_id, workspace_id, user_id, role,
			  invited_by_user_id, invited_at, joined_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			pubID[:], args.WorkspaceID, args.UserID, string(args.Role),
			invitedByArg,
			sql.NullTime{Time: invited, Valid: true},
			sql.NullTime{Time: now, Valid: true})
		if err != nil {
			return res, fmt.Errorf("memberkit: insert member: %w", err)
		}
		id, err := ins.LastInsertId()
		if err != nil {
			return res, fmt.Errorf("memberkit: insert member LastInsertId: %w", err)
		}
		res.MemberID = uint32(id) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative and workspace_members.id is INT UNSIGNED
		res.MemberPublicID = pubID
		res.CreatedMember = true
	default:
		return res, fmt.Errorf("memberkit: find member: %w", err)
	}

	// Step 2: ensure personal calendar if asked. Skipped on re-enable
	// because the calendar already exists.
	if args.EnsurePersonalCalendar {
		calID, cerr := findPersonalCalendar(ctx, tx, args.WorkspaceID, args.UserID)
		switch {
		case cerr == nil:
			res.PersonalCalendarID = calID
		case errors.Is(cerr, sql.ErrNoRows):
			name := findUserDisplayName(ctx, tx, args.UserID)
			calID, err := createCalendar(ctx, tx, args.WorkspaceID, "personal", name, "#4285F4", args.UserID, "")
			if err != nil {
				return res, err
			}
			if err := createSubscription(ctx, tx, args.WorkspaceID, calID, args.UserID, "#4285F4"); err != nil {
				return res, err
			}
			res.PersonalCalendarID = calID
			res.CreatedCalendar = true
		default:
			return res, fmt.Errorf("memberkit: find personal calendar: %w", cerr)
		}
	}

	// Step 4: emit the audit event. Inviter (if any) is the actor,
	// otherwise the user themselves (self-registration).
	actor := args.InvitedByUserID
	if actor == 0 {
		actor = args.UserID
	}
	userPub, err := userPublicID(ctx, tx, args.UserID)
	if err != nil {
		return res, err
	}
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        eventbus.WorkspaceMemberAdded,
		WorkspaceID: args.WorkspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"userId":          userPub.String(),
			"role":            string(args.Role),
			"reenabled":       res.ReenabledMember,
			"createdCalendar": res.CreatedCalendar,
		},
	}); err != nil {
		return res, fmt.Errorf("memberkit: append event: %w", err)
	}

	return res, nil
}
