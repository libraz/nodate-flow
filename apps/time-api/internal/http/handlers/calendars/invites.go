package calendars

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// --- Input/Output types ---

// CreateInviteInput is the input for creating a calendar invite link.
type CreateInviteInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Role      string     `json:"role" enum:"manager,editor,viewer" doc:"Role granted on acceptance"`
		MaxUses   *int32     `json:"maxUses,omitempty" required:"false" doc:"Maximum number of uses"`
		ExpiresAt *time.Time `json:"expiresAt,omitempty" required:"false" doc:"Expiration time"`
	}
}

// InviteResponse is the JSON representation of a calendar invite.
type InviteResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	Role      string `json:"role"`
	MaxUses   *int32 `json:"maxUses,omitempty"`
	UseCount  uint32 `json:"useCount"`
	ExpiresAt *int64 `json:"expiresAt,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// CreateInviteOutput is the response for the create invite endpoint.
type CreateInviteOutput struct {
	Body InviteResponse
}

// ListInvitesInput is the input for listing calendar invites.
type ListInvitesInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// ListInvitesOutput is the response for the list invites endpoint.
type ListInvitesOutput struct {
	Body struct {
		Invites []InviteResponse `json:"invites"`
	}
}

// RevokeInviteInput is the input for revoking a calendar invite.
type RevokeInviteInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	InvId string `path:"invId" doc:"Invite public ID"`
}

// RevokeInviteOutput is the response for the revoke invite endpoint.
type RevokeInviteOutput struct {
	Body struct {
		Revoked bool `json:"revoked"`
	}
}

// AcceptInviteInput is the input for accepting a calendar invite.
type AcceptInviteInput struct {
	Token string `path:"token" doc:"Invite token"`
}

// AcceptInviteOutput is the response for the accept invite endpoint.
type AcceptInviteOutput struct {
	Body struct {
		CalendarID   string `json:"calendarId"`
		CalendarName string `json:"calendarName"`
		Role         string `json:"role"`
	}
}

// --- Handlers ---

// CreateInvite creates a shareable invite link for a calendar.
// Only owners and managers can create invites.
func CreateInvite(deps Deps) func(context.Context, *CreateInviteInput) (*CreateInviteOutput, error) {
	return func(ctx context.Context, input *CreateInviteInput) (*CreateInviteOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}
		if !isOwnerOrManager(sub) {
			return nil, httpErr(apierrors.CalendarCalendarManagerRoleRequired)
		}

		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			return nil, httpErr(apierrors.CalendarInviteTokenGenerateInterrupted)
		}
		token := hex.EncodeToString(tokenBytes)

		invPublicID := types.New()
		params := generated.CreateCalendarInviteParams{
			PublicID:        invPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			CreatedByUserID: actorID,
			TokenHash:       auth.HashOpaque(token),
			Role:            generated.CalendarInvitesRole(input.Body.Role),
		}
		if input.Body.MaxUses != nil {
			params.MaxUses = sql.NullInt32{Int32: *input.Body.MaxUses, Valid: true}
		}
		if input.Body.ExpiresAt != nil {
			params.ExpiresAt = sql.NullTime{Time: *input.Body.ExpiresAt, Valid: true}
		}

		_, err = deps.Queries.CreateCalendarInvite(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreWriteInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.invite.created", &actorID, map[string]any{
			"calendarId": input.CalId,
			"inviteId":   invPublicID.String(),
			"role":       input.Body.Role,
		})

		out := &CreateInviteOutput{}
		out.Body = InviteResponse{
			ID:        invPublicID.String(),
			Token:     token,
			Role:      input.Body.Role,
			UseCount:  0,
			CreatedAt: time.Now().UTC().Unix(),
		}
		if input.Body.MaxUses != nil {
			out.Body.MaxUses = input.Body.MaxUses
		}
		if input.Body.ExpiresAt != nil {
			out.Body.ExpiresAt = int64Ptr(input.Body.ExpiresAt.Unix())
		}

		return out, nil
	}
}

// ListInvites returns all active invite links for a calendar.
// Only owners and managers can list invites.
func ListInvites(deps Deps) func(context.Context, *ListInvitesInput) (*ListInvitesOutput, error) {
	return func(ctx context.Context, input *ListInvitesInput) (*ListInvitesOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}
		if !isOwnerOrManager(sub) {
			return nil, httpErr(apierrors.CalendarCalendarManagerRoleRequired)
		}

		rows, err := deps.Queries.ListCalendarInvites(ctx, generated.ListCalendarInvitesParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteListQueryInterrupted)
		}

		out := &ListInvitesOutput{}
		out.Body.Invites = make([]InviteResponse, len(rows))
		for i, r := range rows {
			resp := InviteResponse{
				ID:        r.PublicID.String(),
				Token:     r.TokenHash,
				Role:      string(r.Role),
				UseCount:  r.UseCount,
				CreatedAt: r.CreatedAt.Unix(),
			}
			if r.MaxUses.Valid {
				v := r.MaxUses.Int32
				resp.MaxUses = &v
			}
			if r.ExpiresAt.Valid {
				resp.ExpiresAt = int64Ptr(r.ExpiresAt.Time.Unix())
			}
			out.Body.Invites[i] = resp
		}
		return out, nil
	}
}

// RevokeInvite disables a calendar invite link.
// Only owners and managers can revoke invites.
func RevokeInvite(deps Deps) func(context.Context, *RevokeInviteInput) (*RevokeInviteOutput, error) {
	return func(ctx context.Context, input *RevokeInviteInput) (*RevokeInviteOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}
		if !isOwnerOrManager(sub) {
			return nil, httpErr(apierrors.CalendarCalendarManagerRoleRequired)
		}

		invUID, err := uuid.Parse(input.InvId)
		if err != nil {
			return nil, errInviteNotFound
		}

		err = deps.Queries.DisableCalendarInvite(ctx, generated.DisableCalendarInviteParams{
			PublicID:   types.FromUUID(invUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteStoreRevokeInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.invite.revoked", &actorID, map[string]any{
			"calendarId": input.CalId,
			"inviteId":   input.InvId,
		})

		out := &RevokeInviteOutput{}
		out.Body.Revoked = true
		return out, nil
	}
}

// AcceptInvite accepts a calendar invite token. The authenticated user is added
// as a member with the role specified in the invite.
func AcceptInvite(deps Deps) func(context.Context, *AcceptInviteInput) (*AcceptInviteOutput, error) {
	return func(ctx context.Context, input *AcceptInviteInput) (*AcceptInviteOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, errAccessDenied
		}

		invite, err := deps.Queries.FindCalendarInviteByTokenHash(ctx, auth.HashOpaque(input.Token))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errInviteNotFound
			}
			return nil, httpErr(apierrors.CalendarInviteStoreLookupInterrupted)
		}

		// Check expiry and use limits.
		if err := validateInvite(invite.ExpiresAt, invite.MaxUses, invite.UseCount); err != nil {
			return nil, err
		}

		// Check if already subscribed.
		_, err = deps.Queries.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
			CalendarID: invite.CalendarID,
			UserID:     actorID,
		})
		if err == nil {
			return nil, httpErr(apierrors.CalendarMemberAlreadySubscribed)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.CalendarSubscriptionMembershipCheckInterrupted)
		}

		// Determine member color.
		members, err := deps.Queries.ListCalendarSubscribers(ctx, generated.ListCalendarSubscribersParams{
			CalendarID:  invite.CalendarID,
			WorkspaceID: invite.WorkspaceID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		color := memberColors[len(members)%len(memberColors)]

		// Map invite role to subscription role.
		subRole := generated.CalendarSubscriptionsRole(invite.Role)

		subPublicID := types.New()
		_, err = deps.Queries.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
			PublicID:     subPublicID,
			WorkspaceID:  invite.WorkspaceID,
			CalendarID:   invite.CalendarID,
			UserID:       actorID,
			Role:         subRole,
			MemberColor:  color,
			DisplayColor: color,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarSubscriptionStoreWriteInterrupted)
		}

		// Increment use count.
		_ = deps.Queries.IncrementCalendarInviteUseCount(ctx, invite.ID)

		out := &AcceptInviteOutput{}
		out.Body.CalendarID = invite.CalendarPublicID.String()
		out.Body.CalendarName = invite.CalendarName
		out.Body.Role = string(subRole)

		_ = eventbus.Append(ctx, deps.DB, invite.WorkspaceID, "calendar.member.joined", &actorID, map[string]any{
			"calendarId": invite.CalendarPublicID.String(),
			"inviteId":   invite.PublicID.String(),
		})

		return out, nil
	}
}
