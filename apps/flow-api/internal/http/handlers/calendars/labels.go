package calendars

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

const (
	// maxCalendarLabels is the number of labels one calendar may hold.
	maxCalendarLabels = 50
	// maxLabelNameRunes is the longest label name, matching the column width.
	maxLabelNameRunes = 50
	// maxLabelSummaryRunes bounds the label name copied into a record payload.
	maxLabelSummaryRunes = 500
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

// normalizeLabelName trims Unicode whitespace (full-width included) and
// reports whether what is left is a usable name: 1 to 50 runes, no control
// characters.
func normalizeLabelName(raw string) (string, bool) {
	name := strings.TrimFunc(raw, unicode.IsSpace)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxLabelNameRunes {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

// invalidLabelField is the refusal for a label field that fails the checks
// the schema cannot express.
func invalidLabelField(field string) error {
	return handlerutil.HTTPErrFromAPIError(apierr.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", field))
}

// labelSummary is the label name as carried in a record payload.
func labelSummary(name string) string {
	if utf8.RuneCountInString(name) <= maxLabelSummaryRunes {
		return name
	}
	return string([]rune(name)[:maxLabelSummaryRunes])
}

// labelFromFields maps a label row to its response. updated_at is NULL until
// the first edit, in which case the creation time stands in for it.
func labelFromFields(publicID types.PublicID, name, color string, updatedAt sql.NullTime, createdAt time.Time) CalendarLabelResponse {
	resp := CalendarLabelResponse{
		ID:        publicID.String(),
		Name:      name,
		Color:     color,
		CreatedAt: createdAt.Unix(),
	}
	resp.UpdatedAt = resp.CreatedAt
	if updatedAt.Valid {
		resp.UpdatedAt = updatedAt.Time.Unix()
	}
	return resp
}

func labelFromListRow(r calendar.ListCalendarLabelsRow) CalendarLabelResponse {
	return labelFromFields(r.PublicID, r.Name, r.Color, r.UpdatedAt, r.CreatedAt)
}

func labelFromFindRow(r calendar.FindCalendarLabelByPublicIdRow) CalendarLabelResponse {
	return labelFromFields(r.PublicID, r.Name, r.Color, r.UpdatedAt, r.CreatedAt)
}

// resolveLabelAdmin resolves the calendar for a label write: manager or
// above, and never a provider-fed system calendar.
func resolveLabelAdmin(ctx context.Context, deps Deps, wsIDStr, calIDStr string) (wsID, actorID uint32, cal calendar.FindCalendarByPublicIdRow, err error) {
	wsID, actorID, err = resolveWorkspace(ctx, deps.Queries, wsIDStr)
	if err != nil {
		return 0, 0, cal, err
	}
	cal, _, err = resolveCalendarAdmin(ctx, deps.CalendarQueries, wsID, actorID, calIDStr)
	if err != nil {
		return 0, 0, cal, err
	}
	if cal.Kind == calendar.CalendarsKindSystem {
		return 0, 0, cal, httpErr(CalendarWriteRefusalSpec(CalendarWriteCalendarReadOnly))
	}
	return wsID, actorID, cal, nil
}

// findLabel resolves a label of the calendar by its public id. A malformed,
// unknown or foreign id is the same not-found.
func findLabel(ctx context.Context, cq *calendar.Queries, calendarID uint32, labelIDStr string) (calendar.FindCalendarLabelByPublicIdRow, error) {
	pid, err := parsePublicID(labelIDStr)
	if err != nil {
		return calendar.FindCalendarLabelByPublicIdRow{}, httpErr(apierrors.CalendarLabelNotFound)
	}
	row, err := cq.FindCalendarLabelByPublicId(ctx, calendar.FindCalendarLabelByPublicIdParams{
		PublicID:   pid,
		CalendarID: calendarID,
	})
	if err != nil {
		return row, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarLabelNotFound, apierrors.CalendarLabelStoreReadInterrupted))
	}
	return row, nil
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
			out.Body.Labels[i] = labelFromListRow(r)
		}
		return out, nil
	}
}

// CreateCalendarLabel adds a label to a calendar. The calendar row is locked
// for the count and the insert so concurrent creates cannot pass the cap
// together.
func CreateCalendarLabel(deps Deps) func(context.Context, *CreateCalendarLabelInput) (*CreateCalendarLabelOutput, error) {
	return func(ctx context.Context, input *CreateCalendarLabelInput) (*CreateCalendarLabelOutput, error) {
		wsID, actorID, cal, err := resolveLabelAdmin(ctx, deps, input.WsID, input.CalID)
		if err != nil {
			return nil, err
		}
		name, ok := normalizeLabelName(input.Body.Name)
		if !ok {
			return nil, invalidLabelField("name")
		}
		color := strings.ToUpper(input.Body.Color)
		publicID := types.New()

		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.CreateCalendarLabel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			q := deps.CalendarQueries.WithTx(tx.RawTx())
			if _, err := q.LockCalendarForLabelWrite(ctx, calendar.LockCalendarForLabelWriteParams{ID: cal.ID, WorkspaceID: wsID}); err != nil {
				answered = httpErr(apierrors.CalendarLabelStoreReadInterrupted)
				return err
			}
			count, err := q.CountCalendarLabels(ctx, cal.ID)
			if err != nil {
				answered = httpErr(apierrors.CalendarLabelStoreReadInterrupted)
				return err
			}
			if count >= maxCalendarLabels {
				answered = httpErr(apierrors.CalendarLabelLimitReached)
				return errAnswered
			}
			if _, err := q.CreateCalendarLabel(ctx, calendar.CreateCalendarLabelParams{
				PublicID:    publicID,
				WorkspaceID: wsID,
				CalendarID:  cal.ID,
				Name:        name,
				Color:       color,
			}); err != nil {
				if handlerutil.IsDuplicateEntry(err) {
					answered = httpErr(apierrors.CalendarLabelNameTaken)
					return errAnswered
				}
				answered = httpErr(apierrors.CalendarLabelStoreWriteInterrupted)
				return err
			}
			return nil
		})
		if txErr != nil {
			if answered != nil {
				return nil, answered
			}
			return nil, httpErr(apierrors.CalendarLabelStoreWriteInterrupted)
		}

		row, err := findLabel(ctx, deps.CalendarQueries, cal.ID, publicID.String())
		if err != nil {
			return nil, err
		}

		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:       sharedbus.CalLabelCreated,
			AuditAction:     "calendar.label.create",
			ResourceType:    "calendar.label",
			ResourceID:      publicID.String(),
			SubjectPublicID: publicID,
			Payload: map[string]any{
				"calendarId": input.CalID,
				"labelId":    publicID.String(),
				"summary":    labelSummary(name),
			},
			CallSite: "calendars.CreateCalendarLabel",
		})

		return &CreateCalendarLabelOutput{Body: labelFromFindRow(row)}, nil
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
		var newName *string
		if input.Body.Name != nil {
			name, ok := normalizeLabelName(*input.Body.Name)
			if !ok {
				return nil, invalidLabelField("name")
			}
			newName = &name
		}

		var answered error
		var updated calendar.FindCalendarLabelByPublicIdRow
		var changed bool
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.UpdateCalendarLabel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered, changed = nil, false
			q := deps.CalendarQueries.WithTx(tx.RawTx())
			if _, err := q.LockCalendarForLabelWrite(ctx, calendar.LockCalendarForLabelWriteParams{ID: cal.ID, WorkspaceID: wsID}); err != nil {
				answered = httpErr(apierrors.CalendarLabelStoreReadInterrupted)
				return err
			}
			cur, err := findLabel(ctx, q, cal.ID, input.LabelID)
			if err != nil {
				answered = err
				return errAnswered
			}
			name, color := cur.Name, cur.Color
			if newName != nil {
				name = *newName
			}
			if input.Body.Color != nil {
				color = strings.ToUpper(*input.Body.Color)
			}
			if name == cur.Name && color == cur.Color {
				updated = cur
				return nil
			}
			if err := q.UpdateCalendarLabel(ctx, calendar.UpdateCalendarLabelParams{Name: name, Color: color, ID: cur.ID}); err != nil {
				if handlerutil.IsDuplicateEntry(err) {
					answered = httpErr(apierrors.CalendarLabelNameTaken)
					return errAnswered
				}
				answered = httpErr(apierrors.CalendarLabelStoreWriteInterrupted)
				return err
			}
			changed = true
			updated, err = findLabel(ctx, q, cal.ID, input.LabelID)
			if err != nil {
				answered = err
				return errAnswered
			}
			return nil
		})
		if txErr != nil {
			if answered != nil {
				return nil, answered
			}
			return nil, httpErr(apierrors.CalendarLabelStoreWriteInterrupted)
		}

		if changed {
			recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
				EventType:       sharedbus.CalLabelUpdated,
				AuditAction:     "calendar.label.update",
				ResourceType:    "calendar.label",
				ResourceID:      updated.PublicID.String(),
				SubjectPublicID: updated.PublicID,
				Payload: map[string]any{
					"calendarId": input.CalID,
					"labelId":    updated.PublicID.String(),
					"summary":    labelSummary(updated.Name),
				},
				CallSite: "calendars.UpdateCalendarLabel",
			})
		}
		return &UpdateCalendarLabelOutput{Body: labelFromFindRow(updated)}, nil
	}
}

// DeleteCalendarLabel removes a label. The links to it go with it through the
// foreign key cascade, so its events fall back to their owner's colour.
func DeleteCalendarLabel(deps Deps) func(context.Context, *DeleteCalendarLabelInput) (*DeleteCalendarLabelOutput, error) {
	return func(ctx context.Context, input *DeleteCalendarLabelInput) (*DeleteCalendarLabelOutput, error) {
		wsID, actorID, cal, err := resolveLabelAdmin(ctx, deps, input.WsID, input.CalID)
		if err != nil {
			return nil, err
		}
		row, err := findLabel(ctx, deps.CalendarQueries, cal.ID, input.LabelID)
		if err != nil {
			return nil, err
		}
		n, err := deps.CalendarQueries.DeleteCalendarLabel(ctx, calendar.DeleteCalendarLabelParams{ID: row.ID, CalendarID: cal.ID})
		if err != nil {
			return nil, httpErr(apierrors.CalendarLabelStoreDeleteInterrupted)
		}
		if n == 0 {
			return nil, httpErr(apierrors.CalendarLabelNotFound)
		}

		recordCalendarChange(ctx, deps, wsID, cal.ID, actorID, mutationlog.Mutation{
			EventType:       sharedbus.CalLabelDeleted,
			AuditAction:     "calendar.label.delete",
			ResourceType:    "calendar.label",
			ResourceID:      row.PublicID.String(),
			SubjectPublicID: row.PublicID,
			Payload: map[string]any{
				"calendarId": input.CalID,
				"labelId":    row.PublicID.String(),
				"summary":    labelSummary(row.Name),
			},
			CallSite: "calendars.DeleteCalendarLabel",
		})
		return &DeleteCalendarLabelOutput{}, nil
	}
}
