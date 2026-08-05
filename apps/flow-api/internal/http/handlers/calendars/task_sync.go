package calendars

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// --- Input/Output types ---

// CreateEventFromTaskInput is the input for the task-to-calendar sync endpoint.
type CreateEventFromTaskInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarTaskSyncTaskNotFound, apierrors.CalendarTaskSyncTaskLookupInterrupted))
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

		// itemkit writes an event row through eventlog inside this
		// transaction, and a deadlock there rolls the whole transaction
		// back, so the retry has to restart the transaction rather than
		// a statement inside it.
		var eventPublicID types.PublicID
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.CreateEventFromTask", nil, func(ctx context.Context, tx *sql.Tx) error {
			id, _, err := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
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
				return err
			}
			eventPublicID = id
			return nil
		})
		if txErr != nil {
			if strings.Contains(txErr.Error(), "itemkit invariant") {
				return nil, httpErr(apierrors.ItemItemkitInvariantViolation)
			}
			return nil, httpErr(apierrors.CalendarTaskSyncStoreWriteInterrupted)
		}

		startUnix := startAt.Unix()
		endUnix := endAt.Unix()
		out := &CreateEventFromTaskOutput{}
		out.Body = EventResponse{
			ID:         eventPublicID.String(),
			Kind:       string(calendar.CalendarEventsKindEvent),
			Visibility: string(calendar.CalendarEventsVisibilityDefault),
			ShowAs:     string(calendar.CalendarEventsShowAsBusy),
			Title:      title,
			AllDay:     false,
			StartAt:    &startUnix,
			EndAt:      &endUnix,
			Timezone:   tzName,
			CreatedAt:  handlerutil.NowUnix(),
		}

		// itemkit already emitted item.scheduled + legacy calendar.event.created.
		// No extra eventbus append here.

		return out, nil
	}
}
