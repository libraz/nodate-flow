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

// --- Input/Output types ---

// AddAttendeesInput is the input for adding attendees to an event.
type AddAttendeesInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		UserIds []string `json:"userIds" doc:"List of user public IDs to add" minItems:"1"`
	}
}

// AttendeeResponse is the JSON representation of an event attendee.
type AttendeeResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarUrl   *string `json:"avatarUrl,omitempty"`
	Rsvp        string  `json:"rsvp"`
	CanEdit     bool    `json:"canEdit"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AddAttendeesOutput is the response for the add attendees endpoint.
type AddAttendeesOutput struct {
	Body struct {
		Attendees []AttendeeResponse `json:"attendees"`
	}
}

// RemoveAttendeeInput is the input for removing an attendee from an event.
type RemoveAttendeeInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	EvtId  string `path:"evtId" doc:"Event public ID"`
	UserId string `path:"userId" doc:"User public ID"`
}

// RemoveAttendeeOutput is the response for the remove attendee endpoint.
type RemoveAttendeeOutput struct {
	Body struct {
		Removed bool `json:"removed"`
	}
}

// UpdateRsvpInput is the input for updating an attendee's RSVP.
type UpdateRsvpInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
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
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	EvtId  string `path:"evtId" doc:"Event public ID"`
	UserId string `path:"userId" doc:"User public ID"`
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
	q *generated.Queries,
	calID uint32,
	evtIDStr string,
) (generated.FindCalendarEventByPublicIdRow, error) {
	evtUID, err := uuid.Parse(evtIDStr)
	if err != nil {
		return generated.FindCalendarEventByPublicIdRow{}, errEventNotFound
	}
	evt, err := q.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
		PublicID:   types.FromUUID(evtUID),
		CalendarID: calID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.FindCalendarEventByPublicIdRow{}, errEventNotFound
		}
		return generated.FindCalendarEventByPublicIdRow{}, err
	}
	return evt, nil
}

// --- Handlers ---

// AddAttendees adds one or more attendees to a calendar event.
func AddAttendees(deps Deps) func(context.Context, *AddAttendeesInput) (*AddAttendeesOutput, error) {
	return func(ctx context.Context, input *AddAttendeesInput) (*AddAttendeesOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		// Only the event owner or calendar owner/manager can add attendees.
		if evt.OwnerUserID != actorID && !isOwnerOrManager(sub) {
			return nil, errForbidden
		}

		out := &AddAttendeesOutput{}
		out.Body.Attendees = make([]AttendeeResponse, 0, len(input.Body.UserIds))

		for _, uidStr := range input.Body.UserIds {
			uid, parseErr := uuid.Parse(uidStr)
			if parseErr != nil {
				continue
			}
			userID, findErr := deps.Queries.FindUserInternalIdByPublicId(ctx, types.FromUUID(uid))
			if findErr != nil {
				continue
			}

			attPublicID := types.New()
			_, createErr := deps.Queries.CreateCalendarEventAttendee(ctx, generated.CreateCalendarEventAttendeeParams{
				PublicID:    attPublicID,
				WorkspaceID: wsID,
				EventID:     evt.ID,
				UserID:      userID,
				Rsvp:        generated.CalendarEventAttendeesRsvpPending,
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
				Rsvp:        string(generated.CalendarEventAttendeesRsvpPending),
				CanEdit:     false,
				CreatedAt:   time.Now().UTC(),
			}
			if profile.AvatarUrl.Valid {
				resp.AvatarUrl = &profile.AvatarUrl.String
			}
			out.Body.Attendees = append(out.Body.Attendees, resp)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.attendees.added", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"count":      len(out.Body.Attendees),
		})

		return out, nil
	}
}

// RemoveAttendee removes an attendee from an event. Only the event owner or
// calendar managers can remove attendees.
func RemoveAttendee(deps Deps) func(context.Context, *RemoveAttendeeInput) (*RemoveAttendeeOutput, error) {
	return func(ctx context.Context, input *RemoveAttendeeInput) (*RemoveAttendeeOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		if evt.OwnerUserID != actorID && !isOwnerOrManager(sub) {
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

		err = deps.Queries.DisableCalendarEventAttendee(ctx, generated.DisableCalendarEventAttendeeParams{
			EventID: evt.ID,
			UserID:  targetUserID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to remove attendee", err)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.attendee.removed", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"userId":     input.UserId,
		})

		out := &RemoveAttendeeOutput{}
		out.Body.Removed = true
		return out, nil
	}
}

// UpdateRsvp updates the authenticated user's RSVP for an event.
func UpdateRsvp(deps Deps) func(context.Context, *UpdateRsvpInput) (*UpdateRsvpOutput, error) {
	return func(ctx context.Context, input *UpdateRsvpInput) (*UpdateRsvpOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		err = deps.Queries.UpdateAttendeeRsvp(ctx, generated.UpdateAttendeeRsvpParams{
			Rsvp:    generated.CalendarEventAttendeesRsvp(input.Body.Rsvp),
			EventID: evt.ID,
			UserID:  actorID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update RSVP", err)
		}

		out := &UpdateRsvpOutput{}
		out.Body.Updated = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.rsvp.updated", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"rsvp":       input.Body.Rsvp,
		})

		return out, nil
	}
}

// ToggleCanEdit toggles the can_edit permission for an attendee.
// Only the event owner can perform this action.
func ToggleCanEdit(deps Deps) func(context.Context, *ToggleCanEditInput) (*ToggleCanEditOutput, error) {
	return func(ctx context.Context, input *ToggleCanEditInput) (*ToggleCanEditOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		if evt.OwnerUserID != actorID {
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

		err = deps.Queries.UpdateAttendeeCanEdit(ctx, generated.UpdateAttendeeCanEditParams{
			CanEdit: input.Body.CanEdit,
			EventID: evt.ID,
			UserID:  targetUserID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update can_edit", err)
		}

		out := &ToggleCanEditOutput{}
		out.Body.Updated = true
		return out, nil
	}
}
