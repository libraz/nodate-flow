package calendars

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/region"
)

// SubscribeSystemCalendarInput is the body for
// POST /workspaces/{wsId}/calendars/subscribe-system.
type SubscribeSystemCalendarInput struct {
	WsId string `path:"wsId" doc:"Workspace public ID"`
	Body struct {
		// Country is an ISO 3166-1 alpha-2 code selecting the holiday feed
		// to subscribe to (e.g. "JP", "US"). The calendar is created on
		// first subscription; subsequent subscriptions for the same country
		// are idempotent.
		Country string `json:"country" minLength:"2" maxLength:"2" pattern:"^[A-Z]{2}$"`
	}
}

// SubscribeSystemCalendarOutput is the response for the subscribe endpoint.
type SubscribeSystemCalendarOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// SubscribeSystemCalendar lets a workspace member opt into the holiday feed
// for an additional country. The underlying calendar is shared by the
// workspace (one row per country) and the caller is subscribed as a viewer.
func SubscribeSystemCalendar(deps Deps) func(context.Context, *SubscribeSystemCalendarInput) (*SubscribeSystemCalendarOutput, error) {
	return func(ctx context.Context, input *SubscribeSystemCalendarInput) (*SubscribeSystemCalendarOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		if err := region.ValidateCountry(input.Body.Country); err != nil || input.Body.Country == "" {
			return nil, httpErr(apierrors.CalendarSubscriptionCountryInvalid)
		}
		if err := SubscribeHolidayCalendar(ctx, deps.Queries, wsID, actorID, input.Body.Country); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &SubscribeSystemCalendarOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
