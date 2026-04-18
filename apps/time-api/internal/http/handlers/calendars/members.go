package calendars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

var memberColors = []string{
	"#4285F4", "#EA4335", "#FBBC04", "#34A853",
	"#FF6D01", "#46BDC6", "#7BAAF7", "#F07B72",
	"#FCD04F", "#57BB8A", "#FF8A65", "#80CBC4",
}

// --- Input/Output types ---

// AddMemberInput is the input for adding a member to a calendar.
type AddMemberInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Email string `json:"email" doc:"Email of the user to add" minLength:"1"`
		Role  string `json:"role" enum:"manager,editor,viewer" doc:"Role to assign"`
	}
}

// MemberResponse is the JSON representation of a calendar member.
type MemberResponse struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	AvatarUrl   *string    `json:"avatarUrl,omitempty"`
	MemberColor string     `json:"memberColor"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// AddMemberOutput is the response for the add member endpoint.
type AddMemberOutput struct {
	Body MemberResponse
}

// ListMembersInput is the input for listing calendar members.
type ListMembersInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// ListMembersOutput is the response for the list members endpoint.
type ListMembersOutput struct {
	Body struct {
		Members []MemberResponse `json:"members"`
	}
}

// UpdateMemberRoleInput is the input for updating a member's role.
type UpdateMemberRoleInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	UserId string `path:"userId" doc:"User public ID"`
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
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	UserId string `path:"userId" doc:"User public ID"`
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}
		if !isOwnerOrManager(sub) {
			return nil, errForbidden
		}

		user, err := deps.Queries.FindUserByEmail(ctx, input.Body.Email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("User not found with that email")
			}
			return nil, huma.Error500InternalServerError("Failed to look up user", err)
		}

		// Check if already subscribed.
		_, err = deps.Queries.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     user.ID,
		})
		if err == nil {
			return nil, huma.Error409Conflict("User is already a member of this calendar")
		}

		// Determine member color based on current member count.
		members, err := deps.Queries.ListCalendarSubscribers(ctx, generated.ListCalendarSubscribersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list members", err)
		}
		color := memberColors[len(members)%len(memberColors)]

		subPublicID := types.New()
		_, err = deps.Queries.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
			PublicID:     subPublicID,
			WorkspaceID:  wsID,
			CalendarID:   cal.ID,
			UserID:       user.ID,
			Role:         generated.CalendarSubscriptionsRole(input.Body.Role),
			MemberColor:  color,
			DisplayColor: color,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to add member", err)
		}

		out := &AddMemberOutput{}
		out.Body = MemberResponse{
			ID:          subPublicID.String(),
			UserID:      user.PublicID.String(),
			DisplayName: user.DisplayName,
			MemberColor: color,
			Role:        input.Body.Role,
			CreatedAt:   time.Now().UTC(),
		}
		if user.AvatarUrl.Valid {
			out.Body.AvatarUrl = &user.AvatarUrl.String
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.member.added", &actorID, map[string]any{
			"calendarId": input.CalId,
			"userId":     user.PublicID.String(),
			"role":       input.Body.Role,
		})

		return out, nil
	}
}

// ListMembers returns all members of a calendar.
func ListMembers(deps Deps) func(context.Context, *ListMembersInput) (*ListMembersOutput, error) {
	return func(ctx context.Context, input *ListMembersInput) (*ListMembersOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		rows, err := deps.Queries.ListCalendarSubscribers(ctx, generated.ListCalendarSubscribersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list members", err)
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
				CreatedAt:   r.CreatedAt,
			}
			if r.AvatarUrl.Valid {
				resp.AvatarUrl = &r.AvatarUrl.String
			}
			out.Body.Members[i] = resp
		}
		return out, nil
	}
}

// UpdateMemberRole changes a member's role. Only owners can change roles.
func UpdateMemberRole(deps Deps) func(context.Context, *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
	return func(ctx context.Context, input *UpdateMemberRoleInput) (*UpdateMemberRoleOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}
		if sub.Role != generated.CalendarSubscriptionsRoleOwner {
			return nil, errForbidden
		}

		targetUID, err := uuid.Parse(input.UserId)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid user ID")
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, huma.Error404NotFound("User not found")
		}

		err = deps.Queries.PatchCalendarSubscription(ctx, generated.PatchCalendarSubscriptionParams{
			Role: generated.NullCalendarSubscriptionsRole{
				CalendarSubscriptionsRole: generated.CalendarSubscriptionsRole(input.Body.Role),
				Valid:                     true,
			},
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update role", err)
		}

		out := &UpdateMemberRoleOutput{}
		out.Body.Updated = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.member.role_changed", &actorID, map[string]any{
			"calendarId": input.CalId,
			"userId":     input.UserId,
			"newRole":    input.Body.Role,
		})

		return out, nil
	}
}

// RemoveMember removes a user from a calendar. Owners can remove anyone,
// members can remove themselves (leave). The last owner cannot be removed.
func RemoveMember(deps Deps) func(context.Context, *RemoveMemberInput) (*RemoveMemberOutput, error) {
	return func(ctx context.Context, input *RemoveMemberInput) (*RemoveMemberOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		targetUID, err := uuid.Parse(input.UserId)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid user ID")
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, huma.Error404NotFound("User not found")
		}

		isSelf := targetUserID == actorID
		isOwner := sub.Role == generated.CalendarSubscriptionsRoleOwner

		if !isSelf && !isOwner {
			return nil, errForbidden
		}

		// Check if target is an owner - prevent removing the last owner.
		targetSub, err := deps.Queries.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("Member not found")
			}
			return nil, huma.Error500InternalServerError("Failed to find member", err)
		}

		if targetSub.Role == generated.CalendarSubscriptionsRoleOwner {
			count, err := deps.Queries.CountCalendarOwners(ctx, cal.ID)
			if err != nil {
				return nil, huma.Error500InternalServerError("Failed to count owners", err)
			}
			if count <= 1 {
				return nil, huma.Error409Conflict("Cannot remove the last owner of a calendar")
			}
		}

		err = deps.Queries.DisableCalendarSubscription(ctx, generated.DisableCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     targetUserID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to remove member", err)
		}

		out := &RemoveMemberOutput{}
		out.Body.Removed = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.member.removed", &actorID, map[string]any{
			"calendarId": input.CalId,
			"userId":     input.UserId,
		})

		return out, nil
	}
}
