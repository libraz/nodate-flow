package calendars

import (
	"context"
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListEvents handles GET /workspaces/{wsId}/calendar-events?start=...&end=...
// and returns events across all calendars the actor subscribes to.
func ListEvents(deps Deps) func(context.Context, *ListEventsInput) (*ListEventsOutput, error) {
	return func(ctx context.Context, in *ListEventsInput) (*ListEventsOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		startTime, err := time.Parse("2006-01-02", in.Start)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		endTime, err := time.Parse("2006-01-02", in.End)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		// end date is exclusive, so add a day
		endTime = endTime.AddDate(0, 0, 1)

		rows, err := deps.Queries.ListCalendarEventsAcrossCalendars(ctx, generated.ListCalendarEventsAcrossCalendarsParams{
			UserID:      uid,
			WorkspaceID: ws.ID,
			StartAt:     endTime,
			EndAt:       startTime,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListEventsOutput{}
		out.Body.Events = make([]EventDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Events = append(out.Body.Events, EventDTO{
				ID:         r.PublicID.String(),
				CalendarID: r.CalendarPublicID.String(),
				Kind:       string(r.Kind),
				Visibility: string(r.Visibility),
				ShowAs:     string(r.ShowAs),
				Title:      r.Title,
				AllDay:     r.AllDay,
				StartAt:    r.StartAt.Format(time.RFC3339),
				EndAt:      r.EndAt.Format(time.RFC3339),
				Timezone:   r.Timezone,
				Location:   nullStr(r.Location),
				BlockLabel: nullStr(r.BlockLabel),
			})
		}
		return out, nil
	}
}

// CreateEvent handles POST /workspaces/{wsId}/calendars/{calId}/events.
func CreateEvent(deps Deps) func(context.Context, *CreateEventInput) (*CreateEventOutput, error) {
	return func(ctx context.Context, in *CreateEventInput) (*CreateEventOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		calPub, err := types.Parse(in.CalID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		cal, err := deps.Queries.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
			PublicID:    calPub,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		startAt, err := time.Parse(time.RFC3339, in.Body.StartAt)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		endAt, err := time.Parse(time.RFC3339, in.Body.EndAt)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		kind := generated.CalendarEventsKindEvent
		if in.Body.Kind != "" {
			kind = generated.CalendarEventsKind(in.Body.Kind)
		}
		showAs := generated.CalendarEventsShowAsBusy
		if in.Body.ShowAs != "" {
			showAs = generated.CalendarEventsShowAs(in.Body.ShowAs)
		}

		pubID := types.New()
		_, err = deps.Queries.CreateCalendarEvent(ctx, generated.CreateCalendarEventParams{
			PublicID:             pubID,
			WorkspaceID:          ws.ID,
			CalendarID:           cal.ID,
			Kind:                 kind,
			Visibility:           generated.CalendarEventsVisibilityDefault,
			ShowAs:               showAs,
			Title:                in.Body.Title,
			AllDay:               in.Body.AllDay,
			StartAt:              startAt,
			EndAt:                endAt,
			Timezone:             in.Body.Timezone,
			Location:             sql.NullString{String: in.Body.Location, Valid: in.Body.Location != ""},
			Memo:                 sql.NullString{String: in.Body.Memo, Valid: in.Body.Memo != ""},
			OwnerUserID:          uid,
			CreatedByUserID:      uid,
			RecurrenceRule:       nil,
			RecurrenceExceptions: nil,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &CreateEventOutput{}
		out.Body = EventDTO{
			ID:         pubID.String(),
			CalendarID: calPub.String(),
			Kind:       string(kind),
			Visibility: "default",
			ShowAs:     string(showAs),
			Title:      in.Body.Title,
			AllDay:     in.Body.AllDay,
			StartAt:    startAt.Format(time.RFC3339),
			EndAt:      endAt.Format(time.RFC3339),
			Timezone:   in.Body.Timezone,
			Location:   in.Body.Location,
			Memo:       in.Body.Memo,
		}
		return out, nil
	}
}

// PatchEvent handles PATCH /workspaces/{wsId}/calendars/{calId}/events/{eventId}.
func PatchEvent(deps Deps) func(context.Context, *PatchEventInput) (*PatchEventOutput, error) {
	return func(ctx context.Context, in *PatchEventInput) (*PatchEventOutput, error) {
		_, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		calPub, err := types.Parse(in.CalID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		cal, err := deps.Queries.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
			PublicID:    calPub,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		evtPub, err := types.Parse(in.EventID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		params := generated.PatchCalendarEventParams{
			PublicID:   evtPub,
			CalendarID: cal.ID,
		}
		if in.Body.Title != nil {
			params.Title = sql.NullString{String: *in.Body.Title, Valid: true}
		}
		if in.Body.AllDay != nil {
			params.AllDay = sql.NullBool{Bool: *in.Body.AllDay, Valid: true}
		}
		if in.Body.StartAt != nil {
			t, err := time.Parse(time.RFC3339, *in.Body.StartAt)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			params.StartAt = sql.NullTime{Time: t, Valid: true}
		}
		if in.Body.EndAt != nil {
			t, err := time.Parse(time.RFC3339, *in.Body.EndAt)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			params.EndAt = sql.NullTime{Time: t, Valid: true}
		}
		if in.Body.Timezone != nil {
			params.Timezone = sql.NullString{String: *in.Body.Timezone, Valid: true}
		}
		if in.Body.Location != nil {
			params.Location = sql.NullString{String: *in.Body.Location, Valid: true}
		}
		if in.Body.Memo != nil {
			params.Memo = sql.NullString{String: *in.Body.Memo, Valid: true}
		}
		if in.Body.Kind != nil {
			params.Kind = generated.NullCalendarEventsKind{
				CalendarEventsKind: generated.CalendarEventsKind(*in.Body.Kind),
				Valid:              true,
			}
		}
		if in.Body.ShowAs != nil {
			params.ShowAs = generated.NullCalendarEventsShowAs{
				CalendarEventsShowAs: generated.CalendarEventsShowAs(*in.Body.ShowAs),
				Valid:                true,
			}
		}

		if err := deps.Queries.PatchCalendarEvent(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Re-fetch to return updated row.
		row, err := deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:   evtPub,
			CalendarID: cal.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &PatchEventOutput{}
		out.Body = EventDTO{
			ID:         row.PublicID.String(),
			CalendarID: calPub.String(),
			Kind:       string(row.Kind),
			Visibility: string(row.Visibility),
			ShowAs:     string(row.ShowAs),
			Title:      row.Title,
			AllDay:     row.AllDay,
			StartAt:    row.StartAt.Format(time.RFC3339),
			EndAt:      row.EndAt.Format(time.RFC3339),
			Timezone:   row.Timezone,
			Location:   nullStr(row.Location),
			Memo:       nullStr(row.Memo),
			BlockLabel: nullStr(row.BlockLabel),
		}
		return out, nil
	}
}

// DeleteEvent handles DELETE /workspaces/{wsId}/calendars/{calId}/events/{eventId}.
func DeleteEvent(deps Deps) func(context.Context, *DeleteEventInput) (*DeleteEventOutput, error) {
	return func(ctx context.Context, in *DeleteEventInput) (*DeleteEventOutput, error) {
		_, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		calPub, err := types.Parse(in.CalID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		cal, err := deps.Queries.FindCalendarByPublicId(ctx, generated.FindCalendarByPublicIdParams{
			PublicID:    calPub,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		evtPub, err := types.Parse(in.EventID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		if err := deps.Queries.DisableCalendarEvent(ctx, generated.DisableCalendarEventParams{
			PublicID:   evtPub,
			CalendarID: cal.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &DeleteEventOutput{Body: DeleteEventOutputBody{Ok: true}}, nil
	}
}
