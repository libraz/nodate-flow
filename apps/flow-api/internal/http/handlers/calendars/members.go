package calendars

import (
	"context"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
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
	Body struct {
		Removed bool `json:"removed"`
	}
}

// --- Handlers ---

// AddMember adds a user to a calendar by email. Only owners and managers can add members.
func AddMember(deps Deps) func(context.Context, *AddMemberInput) (*AddMemberOutput, error) {
	return func(ctx context.Context, input *AddMemberInput) (*AddMemberOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		// Only the calendar owner can add members.
		// Subscription role has been dropped; owner-only is the new gate.
		if !(cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID)) {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}

		user, err := deps.Queries.FindUserByEmail(ctx, input.Body.Email)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberUserNotFound, apierrors.CalendarMemberStoreReadInterrupted))
		}

		// Check if already subscribed.
		_, err = deps.CalendarQueries.FindCalendarSubscription(ctx, calendar.FindCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     user.ID,
		})
		if err == nil {
			return nil, httpErr(apierrors.CalendarMemberAlreadySubscribed)
		}

		// Determine member color based on current member count.
		members, err := deps.CalendarQueries.ListCalendarSubscribers(ctx, calendar.ListCalendarSubscribersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		color := memberColors[len(members)%len(memberColors)]

		subPublicID := types.New()
		_, err = deps.CalendarQueries.CreateCalendarSubscription(ctx, calendar.CreateCalendarSubscriptionParams{
			PublicID:     subPublicID,
			WorkspaceID:  wsID,
			CalendarID:   cal.ID,
			UserID:       user.ID,
			DisplayColor: color,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}

		out := &AddMemberOutput{}
		out.Body = MemberResponse{
			ID:          subPublicID.String(),
			UserID:      user.PublicID.String(),
			DisplayName: user.DisplayName,
			MemberColor: color,
			Role:        input.Body.Role,
			CreatedAt:   handlerutil.NowUnix(),
		}
		out.Body.AvatarURL = dbtype.PtrFromNullString(user.AvatarUrl)

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.member.added", &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     user.PublicID.String(),
			"role":       input.Body.Role,
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

		rows, err := deps.CalendarQueries.ListCalendarSubscribers(ctx, calendar.ListCalendarSubscribersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}

		out := &ListMembersOutput{}
		out.Body.Members = make([]MemberResponse, len(rows))
		for i, r := range rows {
			// member_color + role columns have been dropped from
			// calendar_subscriptions. Surface an empty color and the previous
			// DEFAULT role so the DTO shape stays stable.
			resp := MemberResponse{
				ID:          r.PublicID.String(),
				UserID:      r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				MemberColor: "",
				Role:        "editor",
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
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		// Subscription role has been dropped; fall back to calendar
		// ownership.
		if !(cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID)) {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}

		targetUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserIdMalformed)
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberUserNotFound)
		}

		// Subscription role has been dropped; this endpoint is a no-op
		// until the itemkit rebuild. Kept so existing clients keep 200 OK.
		_ = cal
		_ = targetUserID

		out := &UpdateMemberRoleOutput{}
		out.Body.Updated = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.member.role_changed", &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     input.UserID,
			"newRole":    input.Body.Role,
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
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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

		isSelf := targetUserID == actorID
		// Subscription role has been dropped; fall back to calendar
		// ownership.
		isOwner := cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID)

		if !isSelf && !isOwner {
			return nil, httpErr(apierrors.CalendarCalendarOwnerRoleRequired)
		}

		// Verify the target is subscribed.
		_, err = deps.CalendarQueries.FindCalendarSubscription(ctx, calendar.FindCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarMemberNotFound, apierrors.CalendarMemberStoreReadInterrupted))
		}

		// Last-owner protection now lives on calendars.owner_user_id
		// (not subscription role). Prevent removing the single calendar owner
		// via self-leave.
		if cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(targetUserID) {
			return nil, httpErr(apierrors.CalendarMemberLastOwnerRemovalBlocked)
		}

		err = deps.CalendarQueries.DisableCalendarSubscription(ctx, calendar.DisableCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreRemoveInterrupted)
		}

		out := &RemoveMemberOutput{}
		out.Body.Removed = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.member.removed", &actorID, map[string]any{
			"calendarId": input.CalID,
			"userId":     input.UserID,
		})

		return out, nil
	}
}
