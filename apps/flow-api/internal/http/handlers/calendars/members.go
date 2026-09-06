package calendars

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

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

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalMemberAdded, &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     user.PublicID.String(),
			"role":       input.Body.Role,
		}, "calendars.AddMember")

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

		// A manager may not mint owners, and may not touch an existing one.
		// Otherwise the owner-only gate on deleting the calendar is
		// reachable by anyone who can administer membership.
		if roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleOwner) {
			target, terr := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
				CalendarID: cal.ID,
				UserID:     targetUserID,
			})
			if terr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(terr, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
			}
			if newRole == calendar.CalendarMembersRoleOwner ||
				target.Role == calendar.CalendarMembersRoleOwner {
				return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
			}
		}

		// Demoting the last owner would leave the calendar with nobody able
		// to delete it or restore an owner.
		if newRole != calendar.CalendarMembersRoleOwner {
			owners, oerr := deps.CalendarQueries.CountCalendarOwners(ctx, cal.ID)
			if oerr != nil {
				return nil, httpErr(apierrors.CalendarMemberStoreReadInterrupted)
			}
			current, cerr := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
				CalendarID: cal.ID,
				UserID:     targetUserID,
			})
			if cerr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(cerr, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
			}
			if current.Role == calendar.CalendarMembersRoleOwner && owners <= 1 {
				return nil, httpErr(apierrors.CalendarMemberLastOwnerRemovalBlocked)
			}
		}

		res, err := deps.CalendarQueries.UpdateCalendarMemberRole(ctx, calendar.UpdateCalendarMemberRoleParams{
			Role:       newRole,
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			// Either no live membership, or the role already matched. The
			// former must not report success; distinguish by looking.
			if _, ferr := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
				CalendarID: cal.ID,
				UserID:     targetUserID,
			}); ferr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(ferr, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
			}
		}

		out := &UpdateMemberRoleOutput{}
		out.Body.Updated = true

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalMemberRoleChanged, &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     input.UserID,
			"newRole":    input.Body.Role,
		}, "calendars.UpdateMemberRole")

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

		target, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
		}

		// Leaving is always allowed; removing someone else needs manager or
		// owner, and only an owner may remove another owner.
		isSelf := targetUserID == actorID
		if !isSelf {
			if roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleManager) {
				return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
			}
			if target.Role == calendar.CalendarMembersRoleOwner &&
				roleRank(actorMember.Role) < roleRank(calendar.CalendarMembersRoleOwner) {
				return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
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
		if target.Role == calendar.CalendarMembersRoleOwner {
			owners, oerr := deps.CalendarQueries.CountCalendarOwners(ctx, cal.ID)
			if oerr != nil {
				return nil, httpErr(apierrors.CalendarMemberStoreReadInterrupted)
			}
			if owners <= 1 {
				return nil, httpErr(apierrors.CalendarMemberLastOwnerRemovalBlocked)
			}
		}

		// affected-rows: not-applicable — FindCalendarMember above already
		// answered for a user who is not a member of this calendar, and the
		// role checks between the two ran against the row it returned.
		if _, err = deps.CalendarQueries.DisableCalendarMember(ctx, calendar.DisableCalendarMemberParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreRemoveInterrupted)
		}

		out := &RemoveMemberOutput{}
		out.Body.Removed = true

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalMemberRemoved, &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     input.UserID,
		}, "calendars.RemoveMember")

		return out, nil
	}
}
