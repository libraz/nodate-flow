package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// defaultSubscriptionColor mirrors the calendar_subscriptions.display_color
// column default. A subscription row created by a patch that only sets, say,
// visibility has to start somewhere, and it starts where the schema would
// have put it.
const defaultSubscriptionColor = "#4285F4"

// --- Input/Output types ---

// ListDiscoverableCalendarsInput is the input for the discoverable calendars
// endpoint. It is workspace-scoped: the actor must be a workspace member but
// is, by definition, NOT yet a member of the calendars being listed.
type ListDiscoverableCalendarsInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
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
	OwnerAvatarURL   *string `json:"ownerAvatarUrl,omitempty"`
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Visible      *bool   `json:"visible,omitempty" required:"false" doc:"Whether events from this calendar are rendered for the caller"`
		DisplayColor *string `json:"displayColor,omitempty" required:"false" maxLength:"7" doc:"Caller-specific display color (hex)"`
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}

		rows, err := deps.CalendarQueries.ListDiscoverableCalendarsInWorkspace(ctx, calendar.ListDiscoverableCalendarsInWorkspaceParams{
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
			resp.Description = dbtype.PtrFromNullString(r.Description)
			resp.OwnerAvatarURL = dbtype.PtrFromNullString(r.OwnerAvatarUrl)
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}

		// Manual calendar lookup: calMW is intentionally not applied here,
		// because the actor is, by definition, not yet a calendar member.
		calUID, err := uuid.Parse(input.CalID)
		if err != nil {
			return nil, errCalendarNotFound
		}
		cal, err := deps.CalendarQueries.FindCalendarByPublicId(ctx, calendar.FindCalendarByPublicIdParams{
			PublicID:    types.FromUUID(calUID),
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errCalendarNotFound
			}
			return nil, httpErr(apierrors.CalendarCalendarStoreReadInterrupted)
		}

		// Already a member? Return idempotent success.
		if _, err := deps.CalendarQueries.FindCalendarMember(ctx, calendar.FindCalendarMemberParams{
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

		// Same colour rotation as AddMember, so members fan out
		// predictably however they joined.
		count, err := deps.CalendarQueries.CountCalendarMembers(ctx, calendar.CountCalendarMembersParams{
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemberListQueryInterrupted)
		}
		color := memberColors[int(count)%len(memberColors)]

		// Self-service joining grants the least privilege that makes the
		// calendar useful. Anything more would let a workspace member vote
		// themselves write access to a calendar nobody invited them to.
		if _, err := deps.CalendarQueries.UpsertCalendarMember(ctx, calendar.UpsertCalendarMemberParams{
			PublicID:    types.New(),
			WorkspaceID: wsID,
			CalendarID:  cal.ID,
			UserID:      actorID,
			Role:        calendar.CalendarMembersRoleViewer,
			MemberColor: color,
		}); err != nil {
			return nil, httpErr(apierrors.CalendarMemberStoreWriteInterrupted)
		}

		// The subscription is the caller's own, so the calendar is what
		// identifies it: the actor column already names whose it is.
		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalendarSubscribed,
			AuditAction:  "calendar.subscription.create",
			ResourceType: "calendar.subscription",
			ResourceID:   input.CalID,
			Payload: map[string]any{
				"calendarId": input.CalID,
			},
			CallSite: "calendars.SelfSubscribe",
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		// The write is an upsert: membership lives in calendar_members, so the
		// first time someone recolours or hides a layer there is no
		// subscription row yet. The New* values feed the insert path and start
		// from the column defaults, because a row being created has no earlier
		// preference to carry over; the nullable params feed the duplicate-key
		// branch, where NULL leaves an omitted field as stored.
		params := calendar.PatchCalendarSubscriptionParams{
			PublicID:        types.New(),
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			UserID:          actorID,
			NewDisplayColor: defaultSubscriptionColor,
			NewVisible:      true,
			NewSortWeight:   0,
		}
		if input.Body.Visible != nil {
			params.Visible = sql.NullBool{Bool: *input.Body.Visible, Valid: true}
			params.NewVisible = *input.Body.Visible
		}
		if input.Body.DisplayColor != nil {
			params.DisplayColor = sql.NullString{String: *input.Body.DisplayColor, Valid: true}
			params.NewDisplayColor = *input.Body.DisplayColor
		}
		if input.Body.SortWeight != nil {
			weight := int32(*input.Body.SortWeight) //#nosec G115 -- SortWeight request-validated to a 32-bit signed range
			params.SortWeight = sql.NullInt32{Int32: weight, Valid: true}
			params.NewSortWeight = weight
		}

		// The count is deliberately not inspected for a not-found: the upsert
		// has no predicate that can miss, so 0 rows means the stored
		// preferences already equalled the requested ones. That is a
		// successful no-op, and the caller's state matches what they asked for
		// either way.
		if _, err := deps.CalendarQueries.PatchCalendarSubscription(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarSubscriptionStoreWriteInterrupted)
		}

		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:    eventbus.CalendarSubscriptionUpdated,
			AuditAction:  "calendar.subscription.update",
			ResourceType: "calendar.subscription",
			ResourceID:   input.CalID,
			Payload: map[string]any{
				"calendarId": input.CalID,
			},
			CallSite: "calendars.PatchOwnSubscription",
		})

		out := &PatchOwnSubscriptionOutput{}
		out.Body.Updated = true
		return out, nil
	}
}
