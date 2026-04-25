package calendars

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/itemkit"
)

// --- Input/Output types ---

// CreateEventFromTaskInput is the input for the task-to-calendar sync endpoint.
type CreateEventFromTaskInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		TaskID   string `json:"taskId" doc:"Task public ID (UUID)"`
		Timezone string `json:"timezone,omitempty" doc:"IANA timezone (e.g. America/New_York). Defaults to the caller's user or workspace timezone when omitted, falling back to UTC."`
	}
}

// CreateEventFromTaskOutput is the response for the task-to-calendar sync endpoint.
type CreateEventFromTaskOutput struct {
	Body EventResponse
}

// CreateEventFromTask creates a calendar event from an existing task.
// It delegates the cross-table write (inserting calendar_events +
// mirroring tasks.due_on) to itemkit.ScheduleTask so the task and
// event move in lockstep inside one transaction.
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

		// Ws members have edit access; event-level visibility is the
		// real ACL (applied later).

		taskUID, err := uuid.Parse(input.Body.TaskID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarTaskSyncTaskIdMalformed)
		}
		taskPublicID := types.FromUUID(taskUID)

		// Raw query: this handler only needs id, title, due_on for the
		// projection, so a small SELECT keeps the dependency surface
		// narrow and avoids pulling the full task sqlc row type.
		var taskID uint32
		var title string
		var dueOn *time.Time
		err = deps.DB.QueryRowContext(ctx,
			`SELECT id, title, due_on FROM tasks WHERE public_id = ? AND workspace_id = ? AND enabled = TRUE`,
			taskPublicID, wsID,
		).Scan(&taskID, &title, &dueOn)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarTaskSyncTaskNotFound)
			}
			return nil, httpErr(apierrors.CalendarTaskSyncTaskLookupInterrupted)
		}

		// Determine timezone from request or caller/workspace preference.
		tzName, tzErr := resolveEffectiveTimezone(ctx, deps.Queries, wsID, actorID, input.Body.Timezone)
		if tzErr != nil {
			return nil, httpErr(apierrors.CalendarTaskSyncTimezoneUnrecognized)
		}
		loc, locErr := time.LoadLocation(tzName)
		if locErr != nil {
			return nil, httpErr(apierrors.CalendarTaskSyncTimezoneUnrecognized)
		}

		// Pick the base date: prefer the task's due_on, otherwise today.
		// RoleDue is the only role meaningful for task-projected events.
		role := itemkit.RoleDue
		var baseDate time.Time
		if dueOn != nil {
			baseDate = *dueOn
		} else {
			baseDate = time.Now().In(loc)
		}
		startAt := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), 9, 0, 0, 0, loc)
		endAt := startAt.Add(time.Hour)

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.CalendarTaskSyncStoreWriteInterrupted)
		}
		defer func() { _ = tx.Rollback() }()

		eventPublicID, _, err := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
			WorkspaceID: wsID,
			TaskID:      taskID,
			CalendarID:  cal.ID,
			ActorUserID: actorID,
			Role:        role,
			Title:       title,
			StartAt:     startAt,
			EndAt:       endAt,
			Timezone:    tzName,
		})
		if err != nil {
			if strings.Contains(err.Error(), "itemkit invariant") {
				return nil, httpErr(apierrors.ItemItemkitInvariantViolation)
			}
			return nil, httpErr(apierrors.CalendarTaskSyncStoreWriteInterrupted)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarTaskSyncStoreWriteInterrupted)
		}

		startUnix := startAt.Unix()
		endUnix := endAt.Unix()
		out := &CreateEventFromTaskOutput{}
		out.Body = EventResponse{
			ID:         eventPublicID.String(),
			Kind:       string(generated.CalendarEventsKindEvent),
			Visibility: string(generated.CalendarEventsVisibilityDefault),
			ShowAs:     string(generated.CalendarEventsShowAsBusy),
			Title:      title,
			AllDay:     false,
			StartAt:    &startUnix,
			EndAt:      &endUnix,
			Timezone:   tzName,
			CreatedAt:  time.Now().UTC().Unix(),
		}

		// itemkit already emitted item.scheduled + legacy calendar.event.created.
		// No extra eventbus append here.

		return out, nil
	}
}
