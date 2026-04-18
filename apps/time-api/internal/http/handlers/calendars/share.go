package calendars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
)

// --- Input/Output types ---

// GetSharePageInput is the input for the share page endpoint.
type GetSharePageInput struct {
	Token string `path:"token" doc:"Invite token"`
}

// SharePageResponse is the JSON representation of a calendar share page.
type SharePageResponse struct {
	CalendarID    string     `json:"calendarId"`
	CalendarName  string     `json:"calendarName"`
	CalendarKind  string     `json:"calendarKind"`
	CalendarColor string     `json:"calendarColor"`
	Role          string     `json:"role"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	MemberCount   int64      `json:"memberCount"`
}

// GetSharePageOutput is the response for the share page endpoint.
type GetSharePageOutput struct {
	Body SharePageResponse
}

// GetShareEventsInput is the input for the share events endpoint.
type GetShareEventsInput struct {
	Token string    `path:"token" doc:"Invite token"`
	Start time.Time `query:"start" doc:"Range start" required:"true"`
	End   time.Time `query:"end" doc:"Range end" required:"true"`
}

// GetShareEventsOutput is the response for the share events endpoint.
type GetShareEventsOutput struct {
	Body struct {
		Events []EventResponse `json:"events"`
	}
}

// --- Handlers ---

// GetSharePage returns the public-facing calendar preview for an invite token.
func GetSharePage(deps Deps) func(context.Context, *GetSharePageInput) (*GetSharePageOutput, error) {
	return func(ctx context.Context, input *GetSharePageInput) (*GetSharePageOutput, error) {
		row, err := deps.Queries.FindCalendarInviteByTokenPublic(ctx, input.Token)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errInviteNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to look up invite", err)
		}

		out := &GetSharePageOutput{}
		out.Body = SharePageResponse{
			CalendarID:    row.CalendarPublicID.String(),
			CalendarName:  row.CalendarName,
			CalendarKind:  string(row.CalendarKind),
			CalendarColor: row.CalendarColor,
			Role:          string(row.Role),
			MemberCount:   row.MemberCount,
		}
		if row.ExpiresAt.Valid {
			out.Body.ExpiresAt = &row.ExpiresAt.Time
		}
		return out, nil
	}
}

// GetShareEvents returns events visible through an invite token.
func GetShareEvents(deps Deps) func(context.Context, *GetShareEventsInput) (*GetShareEventsOutput, error) {
	return func(ctx context.Context, input *GetShareEventsInput) (*GetShareEventsOutput, error) {
		invite, err := deps.Queries.FindCalendarInviteByToken(ctx, input.Token)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errInviteNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to look up invite", err)
		}

		if err := validateInvite(invite.ExpiresAt, invite.MaxUses, invite.UseCount); err != nil {
			return nil, err
		}

		rows, err := deps.Queries.ListCalendarEventsByRange(ctx, generated.ListCalendarEventsByRangeParams{
			CalendarID: invite.CalendarID,
			StartAt:    input.End,
			EndAt:      input.Start,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list events", err)
		}

		out := &GetShareEventsOutput{}
		out.Body.Events = make([]EventResponse, len(rows))
		for i, r := range rows {
			out.Body.Events[i] = eventFromRangeRow(r)
		}
		return out, nil
	}
}
