package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/itemkit"
)

// CreateTaskEventLink handles POST /tasks/{id}/links. Delegates the
// cross-table write to itemkit.LinkTaskToEvent so the insert + events
// log row move in lockstep.
func CreateTaskEventLink(deps Deps) func(context.Context, *CreateTaskEventLinkInput) (*CreateTaskEventLinkOutput, error) {
	return func(ctx context.Context, in *CreateTaskEventLinkInput) (*CreateTaskEventLinkOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		eventPub, err := types.Parse(in.Body.EventID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}
		// Resolve the event's internal id within the same workspace.
		var eventID uint32
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT id FROM calendar_events
			 WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
			 LIMIT 1`,
			ws.ID, eventPub,
		).Scan(&eventID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarEventNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		linkPub, _, err := itemkit.LinkTaskToEvent(ctx, tx, itemkit.LinkTaskToEventArgs{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			EventID:     eventID,
			Relation:    itemkit.Relation(in.Body.Relation),
			ActorUserID: actorID,
			SortWeight:  in.Body.SortWeight,
		})
		if err != nil {
			return nil, translateItemkitTaskError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.event_link.add",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_event_link",
				ResourceID:   linkPub.String(),
			})
		}

		return &CreateTaskEventLinkOutput{Body: TaskEventLink{
			ID:         linkPub.String(),
			Relation:   in.Body.Relation,
			SortWeight: in.Body.SortWeight,
			EventID:    eventPub.String(),
			TaskID:     task.PublicID.String(),
		}}, nil
	}
}

// DeleteTaskEventLink handles DELETE /tasks/{id}/links/{linkId}.
func DeleteTaskEventLink(deps Deps) func(context.Context, *DeleteTaskEventLinkInput) (*DeleteTaskEventLinkOutput, error) {
	return func(ctx context.Context, in *DeleteTaskEventLinkInput) (*DeleteTaskEventLinkOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		_, ok = middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		linkPub, err := types.Parse(in.LinkID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		if err := itemkit.UnlinkTaskFromEvent(ctx, tx, itemkit.UnlinkTaskFromEventArgs{
			WorkspaceID: ws.ID,
			LinkID:      linkPub,
			ActorUserID: actorID,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarEventNotFound)
			}
			return nil, translateItemkitTaskError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.event_link.remove",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_event_link",
				ResourceID:   linkPub.String(),
			})
		}

		out := &DeleteTaskEventLinkOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// ListLinkedEvents handles GET /tasks/{id}/linked-events. Returns the
// events that the task is linked to via task_event_links, optionally
// filtered by relation.
func ListLinkedEvents(deps Deps) func(context.Context, *ListLinkedEventsInput) (*ListLinkedEventsOutput, error) {
	return func(ctx context.Context, in *ListLinkedEventsInput) (*ListLinkedEventsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		rows, err := deps.Queries.ListLinkedEventsForTask(ctx, generated.ListLinkedEventsForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Relation:    generated.TaskEventLinksRelation(in.Relation),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListLinkedEventsOutput{}
		out.Body.Links = make([]TaskEventLink, 0, len(rows))
		for _, r := range rows {
			link := TaskEventLink{
				ID:            r.LinkPublicID.String(),
				Relation:      string(r.Relation),
				SortWeight:    r.SortWeight,
				CreatedAt:     r.LinkCreatedAt.Unix(),
				EventID:       r.EventPublicID.String(),
				EventTitle:    r.EventTitle,
				EventAllDay:   r.AllDay,
				EventTimezone: r.Timezone,
				CalendarID:    r.CalendarPublicID.String(),
				CalendarName:  r.CalendarName,
			}
			if r.StartAt.Valid {
				link.EventStartAt = int64Ptr(r.StartAt.Time.Unix())
			}
			if r.EndAt.Valid {
				link.EventEndAt = int64Ptr(r.EndAt.Time.Unix())
			}
			out.Body.Links = append(out.Body.Links, link)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// ListLinkedTasks handles GET /calendar-events/{evtId}/linked-tasks.
// The route lives under flow-api because task_event_links is logically
// a task-side resource; the event side is a lookup key.
func ListLinkedTasks(deps Deps) func(context.Context, *ListLinkedTasksInput) (*ListLinkedTasksOutput, error) {
	return func(ctx context.Context, in *ListLinkedTasksInput) (*ListLinkedTasksOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		eventPub, err := types.Parse(in.EventID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		rows, err := deps.Queries.ListLinkedTasksForEvent(ctx, generated.ListLinkedTasksForEventParams{
			WorkspaceID: ws.ID,
			PublicID:    eventPub,
			Relation:    generated.TaskEventLinksRelation(in.Relation),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListLinkedTasksOutput{}
		out.Body.Links = make([]TaskEventLink, 0, len(rows))
		for _, r := range rows {
			link := TaskEventLink{
				ID:               r.LinkPublicID.String(),
				Relation:         string(r.Relation),
				SortWeight:       r.SortWeight,
				CreatedAt:        r.LinkCreatedAt.Unix(),
				TaskID:           r.TaskPublicID.String(),
				TaskTitle:        r.TaskTitle,
				TaskDerivedState: string(r.TaskDerivedState),
			}
			if r.TaskDueOn.Valid {
				link.TaskDueOn = r.TaskDueOn.Time.UTC().Format("2006-01-02")
			}
			out.Body.Links = append(out.Body.Links, link)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
