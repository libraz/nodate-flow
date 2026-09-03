package calendars

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// --- Input/Output types ---

// AddAttendeesInput is the input for adding attendees to an event.
type AddAttendeesInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		UserIDs []string `json:"userIds" doc:"List of user public IDs to add" minItems:"1"`
	}
}

// AttendeeResponse is the JSON representation of an event attendee.
type AttendeeResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
	Rsvp        string  `json:"rsvp"`
	CanEdit     bool    `json:"canEdit"`
	CreatedAt   int64   `json:"createdAt"`
}

// AddAttendeesOutput is the response for the add attendees endpoint.
type AddAttendeesOutput struct {
	Body struct {
		Attendees []AttendeeResponse `json:"attendees"`
	}
}

// ListAttendeesInput is the input for listing attendees on an event.
type ListAttendeesInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// ListAttendeesOutput is the response for the list attendees endpoint.
type ListAttendeesOutput struct {
	Body struct {
		Attendees []AttendeeResponse `json:"attendees"`
	}
}

// RemoveAttendeeInput is the input for removing an attendee from an event.
type RemoveAttendeeInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	UserID string `path:"userId" doc:"User public ID"`
}

// RemoveAttendeeOutput is the response for the remove attendee endpoint.
type RemoveAttendeeOutput struct {
	Body struct {
		Removed bool `json:"removed"`
	}
}

// UpdateRsvpInput is the input for updating an attendee's RSVP.
type UpdateRsvpInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Rsvp string `json:"rsvp" enum:"pending,accepted,declined,tentative" doc:"RSVP response"`
	}
}

// UpdateRsvpOutput is the response for the update RSVP endpoint.
type UpdateRsvpOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// ToggleCanEditInput is the input for toggling an attendee's can_edit permission.
type ToggleCanEditInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	UserID string `path:"userId" doc:"User public ID"`
	Body   struct {
		CanEdit bool `json:"canEdit" doc:"Whether the attendee can edit the event"`
	}
}

// ToggleCanEditOutput is the response for the toggle can_edit endpoint.
type ToggleCanEditOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// --- Helpers ---

// resolveEvent parses the evtId UUID and returns the event row.
func resolveEvent(
	ctx context.Context,
	cq *calendar.Queries,
	calID uint32,
	wsID uint32,
	evtIDStr string,
) (calendar.FindCalendarEventByPublicIdRow, error) {
	evtUID, err := uuid.Parse(evtIDStr)
	if err != nil {
		return calendar.FindCalendarEventByPublicIdRow{}, errEventNotFound
	}
	evt, err := cq.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    types.FromUUID(evtUID),
		CalendarID:  calID,
		WorkspaceID: wsID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return calendar.FindCalendarEventByPublicIdRow{}, errEventNotFound
		}
		return calendar.FindCalendarEventByPublicIdRow{}, err
	}
	return evt, nil
}

// --- Handlers ---

// AddAttendees adds one or more attendees to a calendar event.
func AddAttendees(deps Deps) func(context.Context, *AddAttendeesInput) (*AddAttendeesOutput, error) {
	return func(ctx context.Context, input *AddAttendeesInput) (*AddAttendeesOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		// Calendar editor is the floor (resolveCalendarWrite); on top of it,
		// only the event's own owner decides who is invited to it.
		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}

		out := &AddAttendeesOutput{}
		out.Body.Attendees = make([]AttendeeResponse, 0, len(input.Body.UserIDs))

		// Who is on the event already, read once rather than once per
		// name in the request. The insert revives a removed attendee
		// with a fresh RSVP, which is right for someone being invited
		// again and wrong for someone who is already listed and has
		// answered: adding a name twice must not quietly withdraw the
		// answer its owner gave. Listing the live rows first is what
		// keeps those two cases apart.
		present := map[uint32]calendar.ListCalendarEventAttendeesRow{}
		existing, listErr := deps.CalendarQueries.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if listErr != nil {
			return nil, httpErr(apierrors.CalendarAttendeeListQueryInterrupted)
		}
		for _, row := range existing {
			present[row.UserID] = row
		}

		for _, uidStr := range input.Body.UserIDs {
			uid, parseErr := uuid.Parse(uidStr)
			if parseErr != nil {
				continue
			}
			userID, findErr := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(uid))
			if findErr != nil {
				continue
			}
			if row, already := present[userID]; already {
				resp := AttendeeResponse{
					ID:          row.PublicID.String(),
					UserID:      uidStr,
					DisplayName: row.DisplayName,
					Rsvp:        string(row.Rsvp),
					CanEdit:     row.CanEdit,
					CreatedAt:   row.CreatedAt.Unix(),
				}
				resp.AvatarURL = dbtype.PtrFromNullString(row.AvatarUrl)
				out.Body.Attendees = append(out.Body.Attendees, resp)
				continue
			}

			attPublicID := types.New()
			_, createErr := deps.CalendarQueries.CreateCalendarEventAttendee(ctx, calendar.CreateCalendarEventAttendeeParams{
				PublicID:    attPublicID,
				WorkspaceID: wsID,
				EventID:     handlerutil.NullInt32From(evt.ID),
				UserID:      userID,
				Rsvp:        calendar.CalendarEventAttendeesRsvpPending,
				CanEdit:     false,
			})
			if createErr != nil {
				continue
			}

			profile, profErr := deps.Queries.FindUserProfileById(ctx, userID)
			if profErr != nil {
				continue
			}

			resp := AttendeeResponse{
				ID:          attPublicID.String(),
				UserID:      uidStr,
				DisplayName: profile.DisplayName,
				Rsvp:        string(calendar.CalendarEventAttendeesRsvpPending),
				CanEdit:     false,
				CreatedAt:   handlerutil.NowUnix(),
			}
			resp.AvatarURL = dbtype.PtrFromNullString(profile.AvatarUrl)
			out.Body.Attendees = append(out.Body.Attendees, resp)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventAttendeeAdded, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"count":      len(out.Body.Attendees),
		}, "calendars.AddAttendees")

		return out, nil
	}
}

// ListAttendees returns all active attendees on a calendar event. Access is
// gated by resolveCalendar (the actor must be a calendar subscriber) and
// resolveEvent (the event must belong to that calendar in the workspace).
func ListAttendees(deps Deps) func(context.Context, *ListAttendeesInput) (*ListAttendeesOutput, error) {
	return func(ctx context.Context, input *ListAttendeesInput) (*ListAttendeesOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		rows, err := deps.CalendarQueries.ListCalendarEventAttendees(ctx, calendar.ListCalendarEventAttendeesParams{
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeListQueryInterrupted)
		}

		out := &ListAttendeesOutput{}
		out.Body.Attendees = make([]AttendeeResponse, len(rows))
		for i, r := range rows {
			resp := AttendeeResponse{
				ID:          r.PublicID.String(),
				UserID:      r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				Rsvp:        string(r.Rsvp),
				CanEdit:     r.CanEdit,
				CreatedAt:   r.CreatedAt.Unix(),
			}
			resp.AvatarURL = dbtype.PtrFromNullString(r.AvatarUrl)
			out.Body.Attendees[i] = resp
		}
		return out, nil
	}
}

// RemoveAttendee removes an attendee from an event. Only the event owner or
// calendar managers can remove attendees.
func RemoveAttendee(deps Deps) func(context.Context, *RemoveAttendeeInput) (*RemoveAttendeeOutput, error) {
	return func(ctx context.Context, input *RemoveAttendeeInput) (*RemoveAttendeeOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}

		targetUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserIdMalformed)
		}
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserNotFound)
		}

		err = deps.CalendarQueries.DisableCalendarEventAttendee(ctx, calendar.DisableCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeStoreRemoveInterrupted)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventAttendeeRemoved, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"userId":     input.UserID,
		}, "calendars.RemoveAttendee")

		out := &RemoveAttendeeOutput{}
		out.Body.Removed = true
		return out, nil
	}
}

// UpdateRsvp updates the authenticated user's RSVP for an event.
func UpdateRsvp(deps Deps) func(context.Context, *UpdateRsvpInput) (*UpdateRsvpOutput, error) {
	return func(ctx context.Context, input *UpdateRsvpInput) (*UpdateRsvpOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		// Nothing above establishes that the actor was invited -- resolveCalendar
		// and resolveEvent only prove the event is visible to them, and seeing
		// an event is not being on its attendee list. The UPDATE cannot answer
		// this either: it reports changed rows, not matched ones, so someone
		// re-confirming the RSVP they already hold is indistinguishable from a
		// stranger writing into nothing. Ask the existence question directly.
		if _, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  actorID,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarAttendeeNotFound)
			}
			return nil, httpErr(apierrors.CalendarAttendeeListQueryInterrupted)
		}

		err = deps.CalendarQueries.UpdateAttendeeRsvp(ctx, calendar.UpdateAttendeeRsvpParams{
			Rsvp:    calendar.CalendarEventAttendeesRsvp(input.Body.Rsvp),
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  actorID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeRsvpUpdateInterrupted)
		}

		out := &UpdateRsvpOutput{}
		out.Body.Updated = true

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventRsvpUpdated, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"rsvp":       input.Body.Rsvp,
		}, "calendars.UpdateRsvp")

		return out, nil
	}
}

// ToggleCanEdit toggles the can_edit permission for an attendee.
// Only the event owner can perform this action.
func ToggleCanEdit(deps Deps) func(context.Context, *ToggleCanEditInput) (*ToggleCanEditOutput, error) {
	return func(ctx context.Context, input *ToggleCanEditInput) (*ToggleCanEditOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		if evt.OwnerUserID != actorID {
			return nil, httpErr(apierrors.CalendarAttendeeOwnerRequiredToToggleEdit)
		}

		targetUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserIdMalformed)
		}
		// Global lookup, kept only to turn the public ID into the internal one.
		// It says nothing about this event.
		targetUserID, err := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(targetUID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeUserNotFound)
		}

		// Attendance is the authority on whether there is anything to grant:
		// edit rights written against a user who is not on this event's list
		// are a permission the owner believes they handed out. The UPDATE's
		// row count cannot stand in for this check, because re-setting can_edit
		// to the value it already holds changes nothing and would look
		// identical to a miss.
		if _, err := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  targetUserID,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarAttendeeNotFound)
			}
			return nil, httpErr(apierrors.CalendarAttendeeListQueryInterrupted)
		}

		err = deps.CalendarQueries.UpdateAttendeeCanEdit(ctx, calendar.UpdateAttendeeCanEditParams{
			CanEdit: input.Body.CanEdit,
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  targetUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttendeeCanEditUpdateInterrupted)
		}

		out := &ToggleCanEditOutput{}
		out.Body.Updated = true
		return out, nil
	}
}
