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

// CreateEventFromTaskInput is the input for the task-to-calendar sync endpoint.
type CreateEventFromTaskInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		TaskID string `json:"taskId" doc:"Task public ID (UUID)"`
	}
}

// CreateEventFromTaskOutput is the response for the task-to-calendar sync endpoint.
type CreateEventFromTaskOutput struct {
	Body EventResponse
}

var errTaskNotFound = huma.Error404NotFound("Task not found")

// CreateEventFromTask creates a calendar event from an existing task.
// It reads the task by public_id using a raw query (since task queries
// belong to flow-api) and creates a linked calendar event.
func CreateEventFromTask(deps Deps) func(context.Context, *CreateEventFromTaskInput) (*CreateEventFromTaskOutput, error) {
	return func(ctx context.Context, input *CreateEventFromTaskInput) (*CreateEventFromTaskOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		taskUID, err := uuid.Parse(input.Body.TaskID)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid taskId format")
		}
		taskPublicID := types.FromUUID(taskUID)

		// Raw query: time-api does not have sqlc-generated task queries.
		var taskID uint32
		var title string
		var eventOn, dueOn *time.Time
		err = deps.DB.QueryRowContext(ctx,
			`SELECT id, title, event_on, due_on FROM tasks WHERE public_id = ? AND workspace_id = ? AND enabled = TRUE`,
			taskPublicID, wsID,
		).Scan(&taskID, &title, &eventOn, &dueOn)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errTaskNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to look up task", err)
		}

		// Determine start time from task dates.
		jst := time.FixedZone("Asia/Tokyo", 9*60*60)
		var baseDate time.Time
		switch {
		case eventOn != nil:
			baseDate = *eventOn
		case dueOn != nil:
			baseDate = *dueOn
		default:
			baseDate = time.Now().In(jst)
		}
		startAt := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), 9, 0, 0, 0, jst)
		endAt := startAt.Add(time.Hour)

		eventPublicID := types.New()
		params := generated.CreateCalendarEventParams{
			PublicID:        eventPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			Kind:            generated.CalendarEventsKindEvent,
			Visibility:      generated.CalendarEventsVisibilityDefault,
			ShowAs:          generated.CalendarEventsShowAsBusy,
			Title:           title,
			AllDay:          false,
			StartAt:         startAt,
			EndAt:           endAt,
			Timezone:        "Asia/Tokyo",
			OwnerUserID:     actorID,
			CreatedByUserID: actorID,
			TaskID:          sql.NullInt32{Int32: int32(taskID), Valid: true},
		}

		_, err = deps.Queries.CreateCalendarEvent(ctx, params)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to create event from task", err)
		}

		out := &CreateEventFromTaskOutput{}
		out.Body = EventResponse{
			ID:        eventPublicID.String(),
			Kind:      string(generated.CalendarEventsKindEvent),
			Visibility: string(generated.CalendarEventsVisibilityDefault),
			ShowAs:    string(generated.CalendarEventsShowAsBusy),
			Title:     title,
			AllDay:    false,
			StartAt:   startAt,
			EndAt:     endAt,
			Timezone:  "Asia/Tokyo",
			CreatedAt: time.Now().UTC(),
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.created_from_task", &actorID, map[string]any{
			"eventId":    eventPublicID.String(),
			"calendarId": input.CalId,
			"taskId":     input.Body.TaskID,
			"title":      title,
		})

		return out, nil
	}
}
