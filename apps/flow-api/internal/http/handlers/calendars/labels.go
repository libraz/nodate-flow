package calendars

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// CalendarLabelResponse is the JSON representation of a calendar label.
type CalendarLabelResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// ListCalendarLabelsInput is the input for listing a calendar's labels.
type ListCalendarLabelsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
}

// ListCalendarLabelsOutput is the response for the list labels endpoint.
type ListCalendarLabelsOutput struct {
	Body struct {
		Labels []CalendarLabelResponse `json:"labels"`
	}
}

// CreateCalendarLabelInput is the input for creating a calendar label.
type CreateCalendarLabelInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Name  string `json:"name" minLength:"1" maxLength:"50" doc:"Label name; surrounding whitespace is trimmed"`
		Color string `json:"color" maxLength:"7" pattern:"^#[0-9A-Fa-f]{6}$" doc:"Label colour as #RRGGBB"`
	}
}

// CreateCalendarLabelOutput is the response for the create label endpoint.
type CreateCalendarLabelOutput struct {
	Body CalendarLabelResponse
}

// UpdateCalendarLabelInput is the input for updating a calendar label.
type UpdateCalendarLabelInput struct {
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	CalID   string `path:"calId" doc:"Calendar public ID"`
	LabelID string `path:"labelId" doc:"Label public ID"`
	Body    struct {
		Name  *string `json:"name,omitempty" required:"false" minLength:"1" maxLength:"50" doc:"New label name; surrounding whitespace is trimmed"`
		Color *string `json:"color,omitempty" required:"false" maxLength:"7" pattern:"^#[0-9A-Fa-f]{6}$" doc:"New label colour as #RRGGBB"`
	}
}

// UpdateCalendarLabelOutput is the response for the update label endpoint.
type UpdateCalendarLabelOutput struct {
	Body CalendarLabelResponse
}

// DeleteCalendarLabelInput is the input for deleting a calendar label.
type DeleteCalendarLabelInput struct {
	WsID    string `path:"wsId" doc:"Workspace public ID"`
	CalID   string `path:"calId" doc:"Calendar public ID"`
	LabelID string `path:"labelId" doc:"Label public ID"`
}

// DeleteCalendarLabelOutput is the empty response for the delete label endpoint.
type DeleteCalendarLabelOutput struct{}

// labelResponse maps a label to its response. The times are converted here,
// the one place this resource does it.
func labelResponse(l calendarlabels.Label) CalendarLabelResponse {
	return CalendarLabelResponse{
		ID:        l.PublicID.String(),
		Name:      l.Name,
		Color:     l.Color,
		CreatedAt: l.CreatedAt.Unix(),
		UpdatedAt: l.UpdatedAt.Unix(),
	}
}

// resolveLabelAdmin resolves the calendar for a label write through
// [DecideCalendarLabelWrite].
func resolveLabelAdmin(ctx context.Context, deps Deps, wsIDStr, calIDStr string) (wsID, actorID uint32, cal calendar.FindCalendarByPublicIdRow, err error) {
	wsID, actorID, err = resolveWorkspace(ctx, deps.Queries, wsIDStr)
	if err != nil {
		return 0, 0, cal, err
	}
	cal, member, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, calIDStr)
	if err != nil {
		return 0, 0, cal, err
	}
	if refusal := DecideCalendarLabelWrite(member.Role, cal.Kind == calendar.CalendarsKindSystem); refusal != nil {
		return 0, 0, calendar.FindCalendarByPublicIdRow{}, handlerutil.HTTPErrFromAPIError(refusal)
	}
	return wsID, actorID, cal, nil
}

// ListCalendarLabels returns every label of a calendar, oldest first.
func ListCalendarLabels(deps Deps) func(context.Context, *ListCalendarLabelsInput) (*ListCalendarLabelsOutput, error) {
	return func(ctx context.Context, input *ListCalendarLabelsInput) (*ListCalendarLabelsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.CalendarQueries.ListCalendarLabels(ctx, cal.ID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarLabelListQueryInterrupted)
		}
		out := &ListCalendarLabelsOutput{}
		out.Body.Labels = make([]CalendarLabelResponse, len(rows))
		for i, r := range rows {
			out.Body.Labels[i] = labelResponse(calendarlabels.FromListRow(r))
		}
		return out, nil
	}
}

// labelMutation is the record of one label change.
func labelMutation(eventType sharedbus.Kind, action, callSite, calID string, l calendarlabels.Label) mutationlog.Mutation {
	return mutationlog.Mutation{
		EventType:       eventType,
		AuditAction:     action,
		ResourceType:    "calendar.label",
		ResourceID:      l.PublicID.String(),
		SubjectPublicID: l.PublicID,
		Payload: map[string]any{
			"calendarId": calID,
			"labelId":    l.PublicID.String(),
			"summary":    calendarlabels.Summary(l.Name),
		},
		CallSite: callSite,
	}
}

// CreateCalendarLabel adds a label to a calendar.
func CreateCalendarLabel(deps Deps) func(context.Context, *CreateCalendarLabelInput) (*CreateCalendarLabelOutput, error) {
	return func(ctx context.Context, input *CreateCalendarLabelInput) (*CreateCalendarLabelOutput, error) {
		wsID, actorID, cal, err := resolveLabelAdmin(ctx, deps, input.WsID, input.CalID)
		if err != nil {
			return nil, err
		}
		label, refusal := calendarlabels.Create(ctx, deps.DB, deps.CalendarQueries, wsID, cal.ID, input.Body.Name, input.Body.Color, nil)
		if refusal != nil {
			return nil, handlerutil.HTTPErrFromAPIError(refusal)
		}
		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID,
			labelMutation(sharedbus.CalLabelCreated, "calendar.label.create", "calendars.CreateCalendarLabel", input.CalID, label))
		return &CreateCalendarLabelOutput{Body: labelResponse(label)}, nil
	}
}

// UpdateCalendarLabel renames or recolours a label. A request that changes
// nothing writes and records nothing.
func UpdateCalendarLabel(deps Deps) func(context.Context, *UpdateCalendarLabelInput) (*UpdateCalendarLabelOutput, error) {
	return func(ctx context.Context, input *UpdateCalendarLabelInput) (*UpdateCalendarLabelOutput, error) {
		wsID, actorID, cal, err := resolveLabelAdmin(ctx, deps, input.WsID, input.CalID)
		if err != nil {
			return nil, err
		}
		label, changed, refusal := calendarlabels.Update(ctx, deps.DB, deps.CalendarQueries, wsID, cal.ID, input.LabelID, input.Body.Name, input.Body.Color, nil)
		if refusal != nil {
			return nil, handlerutil.HTTPErrFromAPIError(refusal)
		}
		if changed {
			recordCalendarChange(ctx, deps, wsID, cal.ID, actorID,
				labelMutation(sharedbus.CalLabelUpdated, "calendar.label.update", "calendars.UpdateCalendarLabel", input.CalID, label))
		}
		return &UpdateCalendarLabelOutput{Body: labelResponse(label)}, nil
	}
}

// DeleteCalendarLabel removes a label.
func DeleteCalendarLabel(deps Deps) func(context.Context, *DeleteCalendarLabelInput) (*DeleteCalendarLabelOutput, error) {
	return func(ctx context.Context, input *DeleteCalendarLabelInput) (*DeleteCalendarLabelOutput, error) {
		wsID, actorID, cal, err := resolveLabelAdmin(ctx, deps, input.WsID, input.CalID)
		if err != nil {
			return nil, err
		}
		label, refusal := calendarlabels.Delete(ctx, deps.DB, deps.CalendarQueries, cal.ID, input.LabelID, nil)
		if refusal != nil {
			return nil, handlerutil.HTTPErrFromAPIError(refusal)
		}
		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID,
			labelMutation(sharedbus.CalLabelDeleted, "calendar.label.delete", "calendars.DeleteCalendarLabel", input.CalID, label))
		return &DeleteCalendarLabelOutput{}, nil
	}
}
