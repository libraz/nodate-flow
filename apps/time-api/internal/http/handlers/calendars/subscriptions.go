package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

// --- Input/Output types ---

// ListDiscoverableCalendarsInput is the input for the discoverable calendars
// endpoint. It is workspace-scoped: the actor must be a workspace member but
// is, by definition, NOT yet a member of the calendars being listed.
type ListDiscoverableCalendarsInput struct {
	WsId string `path:"wsId" doc:"Workspace public ID"`
}

// DiscoverableCalendarResponse is the JSON representation of a teammate
// personal calendar that the caller can subscribe to.
type DiscoverableCalendarResponse struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	Name             string  `json:"name"`
	Description      *string `json:"description,omitempty"`
	Color            string  `json:"color"`
	OwnerUserID      string  `json:"ownerUserId"`
	OwnerDisplayName string  `json:"ownerDisplayName"`
	OwnerAvatarUrl   *string `json:"ownerAvatarUrl,omitempty"`
	CreatedAt        int64   `json:"createdAt"`
}

// ListDiscoverableCalendarsOutput is the response for the discoverable
// calendars endpoint.
type ListDiscoverableCalendarsOutput struct {
	Body struct {
		Calendars []DiscoverableCalendarResponse `json:"calendars"`
	}
}

// SelfSubscribeInput is the input for the self-subscribe endpoint. The
// calendar lookup is performed manually (not via calMW) because the actor
// is, by definition, not yet a member of the calendar.
type SelfSubscribeInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// SelfSubscribeOutput is the response for the self-subscribe endpoint.
// The shape is intentionally small: callers re-fetch the calendar list
// after subscribing to render the updated row in the right rail.
type SelfSubscribeOutput struct {
	Body struct {
		Subscribed        bool `json:"subscribed"`
		AlreadySubscribed bool `json:"alreadySubscribed"`
	}
}

// PatchOwnSubscriptionInput is the input for updating the caller's own
// subscription preferences for a calendar (visibility, display color,
// sort order in the right rail).
type PatchOwnSubscriptionInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Visible      *bool   `json:"visible,omitempty" required:"false" doc:"Whether events from this calendar are rendered for the caller"`
		DisplayColor *string `json:"displayColor,omitempty" required:"false" doc:"Caller-specific display color (hex)"`
		SortWeight   *int    `json:"sortWeight,omitempty" required:"false" doc:"Caller-specific sort weight in the right-rail list"`
	}
}

// PatchOwnSubscriptionOutput is the response for the self-subscription
// patch endpoint.
type PatchOwnSubscriptionOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// --- Handlers ---

// ListDiscoverableCalendars returns the teammate personal calendars in the
// workspace that the caller is not yet subscribed to. Backs the
// "Add teammate calendar" drawer in the flow-web right rail.
func ListDiscoverableCalendars(deps Deps) func(context.Context, *ListDiscoverableCalendarsInput) (*ListDiscoverableCalendarsOutput, error) {
	return func(ctx context.Context, input *ListDiscoverableCalendarsInput) (*ListDiscoverableCalendarsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}

		rows, err := deps.Queries.ListDiscoverableCalendarsInWorkspace(ctx, generated.ListDiscoverableCalendarsInWorkspaceParams{
			WorkspaceID: wsID,
			ActorID:     int64(actorID),
			ActorID_2:   int64(actorID),
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCalendarListQueryInterrupted)
		}

		out := &ListDiscoverableCalendarsOutput{}
		out.Body.Calendars = make([]DiscoverableCalendarResponse, len(rows))
		for i, r := range rows {
			resp := DiscoverableCalendarResponse{
				ID:               r.PublicID.String(),
				Kind:             string(r.Kind),
				Name:             r.Name,
				Color:            r.Color,
				OwnerUserID:      r.OwnerPublicID.String(),
				OwnerDisplayName: r.OwnerDisplayName,
				CreatedAt:        r.CreatedAt.Unix(),
			}
			if r.Description.Valid {
				resp.Description = &r.Description.String
			}
			if r.OwnerAvatarUrl.Valid {
				resp.OwnerAvatarUrl = &r.OwnerAvatarUrl.String
			}
			out.Body.Calendars[i] = resp
		}
		return out, nil
	}
}

// SelfSubscribe subscribes the caller to a calendar that is visible in the
// workspace. Idempotent: if a subscription already exists the call returns
// 200 with alreadySubscribed=true rather than 409.
func SelfSubscribe(deps Deps) func(context.Context, *SelfSubscribeInput) (*SelfSubscribeOutput, error) {
	return func(ctx context.Context, input *SelfSubscribeInput) (*SelfSubscribeOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}

		// Manual calendar lookup: calMW is intentionally not applied here,
		// because the actor is, by definition, not yet a calendar member.
		calUID, err := uuid.Parse(input.CalId)
		if err != nil {
			return nil, errCalendarNotFound
		}
		cal, err := deps.Queries.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
			PublicID:    types.FromUUID(calUID),
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errCalendarNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		// Already subscribed? Return idempotent success.
		if _, err := deps.Queries.FindCalendarSubscription(ctx, generated.FindCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     actorID,
		}); err == nil {
			out := &SelfSubscribeOutput{}
			out.Body.Subscribed = true
			out.Body.AlreadySubscribed = true
			return out, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.CalendarMemberStoreReadInterrupted)
		}

		// Determine display_color via the same color-rotation pattern used
		// by AddMember so subsequent subscribers fan out predictably.
		members, err := deps.Queries.ListCalendarSubscribers(ctx, generated.ListCalendarSubscribersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		color := memberColors[len(members)%len(memberColors)]

		subPublicID := types.New()
		if _, err := deps.Queries.CreateCalendarSubscription(ctx, generated.CreateCalendarSubscriptionParams{
			PublicID:     subPublicID,
			WorkspaceID:  wsID,
			CalendarID:   cal.ID,
			UserID:       actorID,
			DisplayColor: color,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.subscribed", &actorID, map[string]any{
			"calendarId": input.CalId,
		})

		out := &SelfSubscribeOutput{}
		out.Body.Subscribed = true
		out.Body.AlreadySubscribed = false
		return out, nil
	}
}

// PatchOwnSubscription updates the caller's own subscription preferences
// (visibility, display color, sort weight) for a calendar they are
// already subscribed to. Calendar membership is enforced by calMW.
func PatchOwnSubscription(deps Deps) func(context.Context, *PatchOwnSubscriptionInput) (*PatchOwnSubscriptionOutput, error) {
	return func(ctx context.Context, input *PatchOwnSubscriptionInput) (*PatchOwnSubscriptionOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		params := generated.PatchCalendarSubscriptionParams{
			CalendarID: cal.ID,
			UserID:     actorID,
		}
		if input.Body.Visible != nil {
			params.Visible = sql.NullBool{Bool: *input.Body.Visible, Valid: true}
		}
		if input.Body.DisplayColor != nil {
			params.DisplayColor = sql.NullString{String: *input.Body.DisplayColor, Valid: true}
		}
		if input.Body.SortWeight != nil {
			params.SortWeight = sql.NullInt32{Int32: int32(*input.Body.SortWeight), Valid: true}
		}

		if err := deps.Queries.PatchCalendarSubscription(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarSubscriptionStoreWriteInterrupted)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.subscription.updated", &actorID, map[string]any{
			"calendarId": input.CalId,
		})

		out := &PatchOwnSubscriptionOutput{}
		out.Body.Updated = true
		return out, nil
	}
}
