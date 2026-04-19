package calendars

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// List handles GET /workspaces/{wsId}/calendars and returns the calendars
// the actor is subscribed to.
func List(deps Deps) func(context.Context, *ListCalendarsInput) (*ListCalendarsOutput, error) {
	return func(ctx context.Context, _ *ListCalendarsInput) (*ListCalendarsOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		rows, err := deps.Queries.ListCalendarsForUser(ctx, generated.ListCalendarsForUserParams{
			UserID:      uid,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListCalendarsOutput{}
		out.Body.Calendars = make([]CalendarDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Calendars = append(out.Body.Calendars, rowToCalendar(r))
		}
		return out, nil
	}
}

func rowToCalendar(r generated.ListCalendarsForUserRow) CalendarDTO {
	return CalendarDTO{
		ID:           r.PublicID.String(),
		Kind:         string(r.Kind),
		Name:         r.Name,
		Description:  nullStr(r.Description),
		Color:        r.Color,
		MemberColor:  r.MemberColor,
		DisplayColor: r.DisplayColor,
		CoverURL:     nullStr(r.CoverUrl),
		Role:         string(r.Role),
		Visible:      r.Visible,
		UpdatedAt:    nullTimeUnix(r.UpdatedAt),
		CreatedAt:    r.CreatedAt.Unix(),
	}
}
