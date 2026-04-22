package calendars

import (
	"context"
	"database/sql"
	"encoding/json"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// ListMyCalendarEventsInput is the query for GET /me/calendar-events.
// `start` and `end` accept either a date (YYYY-MM-DD) or an RFC 3339
// datetime, matching the per-workspace cross-calendar endpoint so the
// client can share parsing code. `tz` is currently reserved for a
// future client-hint parameter; the returned start_at / end_at are unix
// seconds (UTC) regardless.
type ListMyCalendarEventsInput struct {
	Start string `query:"start" required:"true" doc:"Range start (inclusive, YYYY-MM-DD or RFC3339)"`
	End   string `query:"end" required:"true" doc:"Range end (exclusive, YYYY-MM-DD or RFC3339)"`
}

// MyCalendarEventResponse is a single row in the cross-workspace
// /me/calendar-events response. It carries workspace context per row so
// the caller can group/filter client-side without a second round-trip
// per workspace.
type MyCalendarEventResponse struct {
	ID                   string           `json:"id"`
	CalendarID           string           `json:"calendarId"`
	WorkspaceID          string           `json:"workspaceId"`
	WorkspaceName        string           `json:"workspaceName"`
	Kind                 string           `json:"kind"`
	Visibility           string           `json:"visibility"`
	ShowAs               string           `json:"showAs"`
	Title                string           `json:"title"`
	AllDay               bool             `json:"allDay"`
	StartAt              *int64           `json:"startAt,omitempty"`
	EndAt                *int64           `json:"endAt,omitempty"`
	Timezone             string           `json:"timezone"`
	Location             *string          `json:"location,omitempty"`
	BlockLabel           *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	UpdatedAt            *int64           `json:"updatedAt,omitempty"`
	CreatedAt            int64            `json:"createdAt"`
}

// ListMyCalendarEventsOutput is the response for GET /me/calendar-events.
type ListMyCalendarEventsOutput struct {
	Body struct {
		Events []MyCalendarEventResponse `json:"events"`
	}
}

// ListMyCalendarEvents handles GET /me/calendar-events?start=&end=. It
// returns every visible event across every calendar the authenticated
// user is subscribed to, in every workspace they are an active member
// of. Pairs with flow-api's GET /me/tasks-with-dates so the unified
// flow-web calendar renders with two cross-service requests instead of
// fanning out per-workspace.
func ListMyCalendarEvents(deps Deps) func(context.Context, *ListMyCalendarEventsInput) (*ListMyCalendarEventsOutput, error) {
	return func(ctx context.Context, input *ListMyCalendarEventsInput) (*ListMyCalendarEventsOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		startTime, err := parseFlexibleTime(input.Start)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}
		endTime, err := parseFlexibleTime(input.End)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}
		if !endTime.After(startTime) {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}

		rows, err := deps.Queries.ListMyCalendarEventsAcrossWorkspaces(ctx, generated.ListMyCalendarEventsAcrossWorkspacesParams{
			UserID:  actorID,
			StartAt: sql.NullTime{Time: endTime, Valid: true},
			EndAt:   sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		recurringRows, err := deps.Queries.ListMyRecurringCalendarEventsAcrossWorkspaces(ctx, generated.ListMyRecurringCalendarEventsAcrossWorkspacesParams{
			UserID:        actorID,
			StartAt:       sql.NullTime{Time: endTime, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		out := &ListMyCalendarEventsOutput{}
		out.Body.Events = make([]MyCalendarEventResponse, 0, len(rows)+len(recurringRows))

		for _, r := range rows {
			resp := MyCalendarEventResponse{
				ID:            r.PublicID.String(),
				CalendarID:    r.CalendarPublicID.String(),
				WorkspaceID:   r.WorkspacePublicID.String(),
				WorkspaceName: r.WorkspaceName,
				Kind:          string(r.Kind),
				Visibility:    string(r.Visibility),
				ShowAs:        string(r.ShowAs),
				Title:         r.Title,
				AllDay:        r.AllDay,
				StartAt:       nullTimeUnixPtr(r.StartAt),
				EndAt:         nullTimeUnixPtr(r.EndAt),
				Timezone:      r.Timezone,
				CreatedAt:     r.CreatedAt.Unix(),
			}
			if r.Location.Valid {
				resp.Location = &r.Location.String
			}
			if r.BlockLabel.Valid {
				resp.BlockLabel = &r.BlockLabel.String
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = int64Ptr(r.UpdatedAt.Time.Unix())
			}
			out.Body.Events = append(out.Body.Events, resp)
		}

		for _, r := range recurringRows {
			resp := MyCalendarEventResponse{
				ID:            r.PublicID.String(),
				CalendarID:    r.CalendarPublicID.String(),
				WorkspaceID:   r.WorkspacePublicID.String(),
				WorkspaceName: r.WorkspaceName,
				Kind:          string(r.Kind),
				Visibility:    string(r.Visibility),
				ShowAs:        string(r.ShowAs),
				Title:         r.Title,
				AllDay:        r.AllDay,
				StartAt:       nullTimeUnixPtr(r.StartAt),
				EndAt:         nullTimeUnixPtr(r.EndAt),
				Timezone:      r.Timezone,
				CreatedAt:     r.CreatedAt.Unix(),
			}
			if r.Location.Valid {
				resp.Location = &r.Location.String
			}
			if r.BlockLabel.Valid {
				resp.BlockLabel = &r.BlockLabel.String
			}
			if r.RecurrenceRule != nil {
				raw := json.RawMessage(r.RecurrenceRule)
				resp.RecurrenceRule = &raw
			}
			if r.RecurrenceEnd.Valid {
				resp.RecurrenceEnd = int64Ptr(r.RecurrenceEnd.Time.Unix())
			}
			if r.RecurrenceExceptions != nil {
				raw := json.RawMessage(r.RecurrenceExceptions)
				resp.RecurrenceExceptions = &raw
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = int64Ptr(r.UpdatedAt.Time.Unix())
			}
			out.Body.Events = append(out.Body.Events, resp)
		}

		return out, nil
	}
}
