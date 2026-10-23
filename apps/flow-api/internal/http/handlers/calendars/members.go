package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
)

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("calendars: answered")

// lockCalendarMemberRole locks a live calendar membership and returns its
// role. sql.ErrNoRows when the user holds no live membership.
func lockCalendarMemberRole(ctx context.Context, tx *dbretry.Tx, calendarID, userID uint32) (calendar.CalendarMembersRole, error) {
	var role calendar.CalendarMembersRole
	err := tx.QueryRowContext(ctx,
		`SELECT role FROM calendar_members WHERE calendar_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1 FOR UPDATE`,
		calendarID, userID).Scan(&role)
	return role, err
}

// ensureCalendarOwnerRemains answers CALENDAR.MEMBER.LAST_OWNER_REMOVAL_BLOCKED
// unless a live owner other than leaving remains on the calendar, through
// the shared memberkit guard that locks the owner rows.
func ensureCalendarOwnerRemains(ctx context.Context, tx *dbretry.Tx, calendarID, leaving uint32) (*apierr.Spec, error) {
	err := memberkit.EnsureOwnerRemains(ctx, tx, memberkit.OwnerSet{
		Query: `SELECT user_id FROM calendar_members WHERE calendar_id = ? AND role = 'owner' AND enabled = TRUE`,
		Args:  []any{calendarID},
	}, leaving)
	if errors.Is(err, memberkit.ErrLastOwner) {
		return apierrors.CalendarMemberLastOwnerRemovalBlocked, nil
	}
	return nil, err
}

var memberColors = []string{
	"#4285F4", "#EA4335", "#FBBC04", "#34A853",
	"#FF6D01", "#46BDC6", "#7BAAF7", "#F07B72",
	"#FCD04F", "#57BB8A", "#FF8A65", "#80CBC4",
}

// --- Input/Output types ---

// AddMemberInput is the input for adding a member to a calendar.
type AddMemberInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Email string `json:"email" doc:"Email of the user to add" minLength:"1"`
		Role  string `json:"role" enum:"manager,editor,viewer" doc:"Role to assign"`
	}
}

// MemberResponse is the JSON representation of a calendar member.
type MemberResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
	MemberColor string  `json:"memberColor"`
	Role        string  `json:"role"`
	CreatedAt   int64   `json:"createdAt"`
}

// AddMemberOutput is the response for the add member endpoint.
type AddMemberOutput struct {
	Body MemberResponse
}

// ListMembersInput is the input for listing calendar members.
type ListMembersInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
}

// ListMembersOutput is the response for the list members endpoint.
type ListMembersOutput struct {
	Body struct {
		Members []MemberResponse `json:"members"`
	}
}

// UpdateMemberRoleInput is the input for updating a member's role.
type UpdateMemberRoleInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	UserID string `path:"userId" doc:"User public ID"`
	Body   struct {
		Role string `json:"role" enum:"owner,manager,editor,viewer" doc:"New role"`
	}
}

// UpdateMemberRoleOutput is the response for the update member role endpoint.
type UpdateMemberRoleOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// RemoveMemberInput is the input for removing a member from a calendar.
type RemoveMemberInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	UserID string `path:"userId" doc:"User public ID"`
}

// RemoveMemberOutput is the response for the remove member endpoint.
type RemoveMemberOutput struct {
	Body CalendarRemoveMemberOutputBody
}

// CalendarRemoveMemberOutputBody is the response body for removing a calendar member.
type CalendarRemoveMemberOutputBody struct {
	Removed bool `json:"removed"`
}

// --- Handlers ---

// AddMember adds a user to a calendar by email. Only owners and managers can add members.
func AddMember(deps Deps) func(context.Context, *AddMemberInput) (*AddMemberOutput, error) {
	return func(ctx context.Context, input *AddMemberInput) (*AddMemberOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarAdmin(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		user, err := deps.Queries.FindUserByEmail(ctx, input.Body.Email)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberUserNotFound, apierrors.CalendarMemberStoreReadInterrupted))
		}

		_, err = deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
			CalendarID: cal.ID,
			UserID:     user.ID,
		})
		if err == nil {
			return nil, httpErr(apierrors.CalendarMemberAlreadySubscribed)
		}

		// Pick the next colour off the palette by member count, so members
		// added in order are visually distinct until the palette wraps.
		count, err := deps.CalendarQueries.CountCalendarMembers(ctx, calendar.CountCalendarMembersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		color := memberColors[int(count)%len(memberColors)]

		memberPublicID := types.New()
		_, err = deps.CalendarQueries.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
			PublicID:        memberPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			UserID:          user.ID,
			Role:            calendar.CalendarMembersRole(input.Body.Role),
			MemberColor:     color,
			InvitedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}

		out := &AddMemberOutput{}
		out.Body = MemberResponse{
			ID:          memberPublicID.String(),
			UserID:      user.PublicID.String(),
			DisplayName: user.DisplayName,
			MemberColor: color,
			Role:        input.Body.Role,
			CreatedAt:   handlerutil.NowUnix(),
		}
		out.Body.AvatarURL = dbtype.PtrFromNullString(user.AvatarUrl)

		// The membership is named by the user it grants, the same way the
		// two operations that change and withdraw it are, so an
		// administrator asking what happened to one person's access reads
		// all three from one resource id.
		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalMemberAdded,
			AuditAction:  "calendar.member.add",
			ResourceType: "calendar.member",
			ResourceID:   user.PublicID.String(),
			Payload: map[string]any{
				"calendarId": input.CalID,
				"userId":     user.PublicID.String(),
				"role":       input.Body.Role,
			},
			CallSite: "calendars.AddMember",
		})

		return out, nil
	}
}

// ListMembers returns all members of a calendar.
func ListMembers(deps Deps) func(context.Context, *ListMembersInput) (*ListMembersOutput, error) {
	return func(ctx context.Context, input *ListMembersInput) (*ListMembersOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		rows, err := deps.CalendarQueries.ListCalendarMembers(ctx, calendar.ListCalendarMembersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}

		out := &ListMembersOutput{}
		out.Body.Members = make([]MemberResponse, len(rows))
		for i, r := range rows {
			resp := MemberResponse{
				ID:          r.PublicID.String(),
				UserID:      r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				MemberColor: r.MemberColor,
				Role:        string(r.Role),
				CreatedAt:   r.CreatedAt.Unix(),
			}
			resp.AvatarURL = dbtype.PtrFromNullString(r.AvatarUrl)
			out.Body.Members[i] = resp
		}
		return out, nil
	}
}

// UpdateMemberRole changes a member's role. Only owners can change roles.
func UpdateMemberRole(deps Deps) func(context.Context, *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
	return func(ctx context.Context, input *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, actorMember, err := resolveCalendarAdmin(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		targetUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserIdMalformed)
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserNotFound)
		}

		newRole := calendar.CalendarMembersRole(input.Body.Role)

		// The role gates and the last-owner guard are decided against rows
		// locked in the transaction that writes, so two owners demoting
		// each other cannot both pass, and a manager cannot act on a member
		// promoted to owner after a stale read.
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.UpdateMemberRole", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			currentRole, err := lockCalendarMemberRole(ctx, tx, cal.ID, targetUserID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.CalendarMemberNotFound)
					return errAnswered
				}
				return err
			}
			// A manager may not mint owners, and may not touch an existing one.
			// Otherwise the owner-only gate on deleting the calendar is
			// reachable by anyone who can administer membership.
			if roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleOwner) &&
				(newRole == calendar.CalendarMembersRoleOwner || currentRole == calendar.CalendarMembersRoleOwner) {
				answered = httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
				return errAnswered
			}
			// Demoting the last owner would leave the calendar with nobody able
			// to delete it or restore an owner.
			if currentRole == calendar.CalendarMembersRoleOwner && newRole != calendar.CalendarMembersRoleOwner {
				if spec, err := ensureCalendarOwnerRemains(ctx, tx, cal.ID, targetUserID); err != nil || spec != nil {
					if spec != nil {
						answered = httpErr(spec)
						return errAnswered
					}
					return err
				}
			}
			// The member row is locked above, so a zero count is a role the
			// member already holds.
			_, err = deps.CalendarQueries.WithTx(tx.RawTx()).UpdateCalendarMemberRole(ctx, calendar.UpdateCalendarMemberRoleParams{
				Role:       newRole,
				CalendarID: cal.ID,
				UserID:     targetUserID,
			})
			return err
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}

		out := &UpdateMemberRoleOutput{}
		out.Body.Updated = true

		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalMemberRoleChanged,
			AuditAction:  "calendar.member.role_change",
			ResourceType: "calendar.member",
			ResourceID:   input.UserID,
			Payload: map[string]any{
				"calendarId": input.CalID,
				"userId":     input.UserID,
				"newRole":    input.Body.Role,
			},
			CallSite: "calendars.UpdateMemberRole",
		})

		return out, nil
	}
}

// RemoveMember removes a user from a calendar. Owners can remove anyone,
// members can remove themselves (leave). The last owner cannot be removed.
func RemoveMember(deps Deps) func(context.Context, *RemoveMemberInput) (*RemoveMemberOutput, error) {
	return func(ctx context.Context, input *RemoveMemberInput) (*RemoveMemberOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, actorMember, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		targetUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserIdMalformed)
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserNotFound)
		}

		// Decided against the member row and the owner rows locked in the
		// transaction that disables, for the same reason as UpdateMemberRole.
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.RemoveMember", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			targetRole, err := lockCalendarMemberRole(ctx, tx, cal.ID, targetUserID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.CalendarMemberNotFound)
					return errAnswered
				}
				return err
			}

			// Leaving is always allowed; removing someone else needs manager or
			// owner, and only an owner may remove another owner.
			isSelf := targetUserID == actorID
			if !isSelf {
				if roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleManager) ||
					(targetRole == calendar.CalendarMembersRoleOwner &&
						roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleOwner)) {
					answered = httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
					return errAnswered
				}
			}

			// The last owner cannot leave or be removed, including by
			// themselves — a calendar with no owner can never regain one.
			//
			// Removing someone from the workspace keeps the same invariant by
			// the other means: memberkit hands a sole-owned calendar to a
			// remaining workspace owner before it retires the grants, because
			// offboarding a person has to succeed. Refusing is only affordable
			// here, where the caller asked about one calendar and can pick
			// another owner first.
			if targetRole == calendar.CalendarMembersRoleOwner {
				if spec, err := ensureCalendarOwnerRemains(ctx, tx, cal.ID, targetUserID); err != nil || spec != nil {
					if spec != nil {
						answered = httpErr(spec)
						return errAnswered
					}
					return err
				}
			}

			// affected-rows: not-applicable — the member row is locked above.
			_, err = deps.CalendarQueries.WithTx(tx.RawTx()).DisableCalendarMember(ctx, calendar.DisableCalendarMemberParams{
				CalendarID: cal.ID,
				UserID:     targetUserID,
			})
			return err
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreRemoveInterrupted)
		}

		out := &RemoveMemberOutput{}
		out.Body.Removed = true

		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalMemberRemoved,
			AuditAction:  "calendar.member.remove",
			ResourceType: "calendar.member",
			ResourceID:   input.UserID,
			Payload: map[string]any{
				"calendarId": input.CalID,
				"userId":     input.UserID,
			},
			CallSite: "calendars.RemoveMember",
		})

		return out, nil
	}
}
