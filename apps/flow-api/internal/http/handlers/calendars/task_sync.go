package calendars

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// taskEventStartHour is the wall-clock hour a task-projected event
// starts at on its due date. A task carries a date and no time, so the
// projection has to choose one; naming it keeps the choice visible
// rather than leaving a literal in the middle of the handler.
const taskEventStartHour = 9

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
		//
		// The task is named in the request body, not in the path, so no
		// RequireTaskAccess ran for it and workspace membership alone is
		// what got the caller here. Layer 4 therefore has to be applied
		// in this statement: without it a member could name any task id
		// in the workspace and read its title back, and the title would
		// also be copied into a calendar event the whole workspace can
		// see. The read goes through v_task_list_all rather than tasks
		// so the shared fragment — spliced, never retyped — anchors on
		// the column names it is written against; the view keeps the
		// enabled-row scope this statement already had and, unlike
		// v_task_list, still admits an archived task.
		wsRole, roleErr := workspaceRoleOf(ctx, deps.Queries, wsID, actorID)
		if roleErr != nil {
			return nil, roleErr
		}
		where := []string{"v.workspace_id = ?", "v.public_id = ?"}
		args := []any{wsID, taskPublicID}
		if visFrag, visArgs := acl.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
			where = append(where, visFrag)
			args = append(args, visArgs...)
		}
		//#nosec G201 -- the only interpolated text is acl.TaskVisibilityFilter's own constant fragment; every value is bound.
		taskQuery := fmt.Sprintf(
			`SELECT v.task_internal_id, v.title, v.due_on FROM v_task_list_all v WHERE %s`,
			strings.Join(where, " AND "))

		var taskID uint32
		var title string
		var dueOn *time.Time
		err = deps.DB.QueryRowContext(ctx, taskQuery, args...).Scan(&taskID, &title, &dueOn)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarTaskSyncTaskNotFound, apierrors.CalendarTaskSyncTaskLookupInterrupted))
		}

		// Determine timezone from request or caller/workspace preference.
		tz, tzErr := resolveEffectiveTimezone(ctx, deps.Queries, wsID, actorID, input.Body.Timezone)
		if tzErr != nil {
			return nil, tzErr
		}

		// Pick the base date: prefer the task's due_on, otherwise today.
		// due_on is a DATE column, so its day is already zone-free;
		// "today" is not, and is the caller's day rather than the
		// server's.
		// RoleDue is the only role meaningful for task-projected events.
		role := itemkit.RoleDue
		var baseDay region.Day
		if dueOn != nil {
			baseDay = region.DayFromDateColumn(*dueOn)
		} else {
			baseDay = region.DayOf(time.Now(), tz)
		}
		startAt := baseDay.At(tz, taskEventStartHour, 0, 0)
		endAt := startAt.Add(time.Hour)

		// itemkit writes an event row through eventlog inside this
		// transaction, and a deadlock there rolls the whole transaction
		// back, so the retry has to restart the transaction rather than
		// a statement inside it.
		var eventPublicID types.PublicID
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.CreateEventFromTask", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			id, _, err := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
				WorkspaceID: wsID,
				TaskID:      taskID,
				CalendarID:  cal.ID,
				ActorUserID: actorID,
				Role:        role,
				Title:       title,
				StartAt:     startAt,
				EndAt:       endAt,
				Timezone:    tz.Name(),
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
			Timezone:   tz.Name(),
			CreatedAt:  handlerutil.NowUnix(),
		}

		// itemkit already emitted item.scheduled + legacy calendar.event.created.
		// No extra eventbus append here.

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.event.create",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.event",
			ResourceID:   eventPublicID.String(),
			Metadata: map[string]any{
				"calendarId": input.CalID,
				"title":      title,
				"kind":       string(calendar.CalendarEventsKindEvent),
				"taskId":     taskPublicID.String(),
			},
		})

		return out, nil
	}
}
