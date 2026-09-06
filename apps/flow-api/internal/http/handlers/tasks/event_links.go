package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// eventVisibilityRefusal answers which refusal reaching an event on a
// calendar of the given standing earns, or nil when the caller may reach
// it. It is the read-side counterpart of [shiftRefusal]: both take the
// same calendarStanding and differ only in the floor they set.
//
// The floor is membership itself, at any role. A calendar's contents are
// visible to every one of its members — the premise the write rule's
// editor floor rests on — so a viewer who may open the event may see
// what hangs off it.
//
// [calendars.DecideCalendarWrite] is deliberately not the rule here.
// Both of its refusals answer a question about writing: the editor floor
// is what separates a member who may change a calendar's contents from
// one who may only read them, and the system-calendar refusal exists
// because a provider feed's rows have no source to reconcile a user's
// write against. Neither says anything about reading, and refusing a
// system calendar to a member who subscribed to it would deny the
// ordinary use of one.
//
// A caller holding no membership is answered with the same not-found an
// unknown event id gets, matching what the shift routes chose. The
// request names an event and never names a calendar, so any other answer
// confirms that the id belongs to a live event on a calendar the caller
// cannot see — and unlike the refusals a member can earn, there is
// nothing this caller could do with the distinction.
func eventVisibilityRefusal(standing *calendarStanding) *apierrors.Spec {
	if standing == nil {
		return apierrors.CalendarEventNotFound
	}
	return nil
}

// resolveVisibleEvent looks up a calendar event by its public id within
// the workspace and refuses unless the actor holds a live grant on the
// calendar it lives on. It returns the event's internal id and its
// parsed public id.
//
// The lookup and the standing behind it come from
// [resolveEventStanding], shared with the write side so the two routes
// cannot come to disagree about which calendar an event lives on or what
// the actor holds there. What stays here is the decision: this route
// applies the read floor.
func resolveVisibleEvent(
	ctx context.Context,
	deps Deps,
	workspaceID, actorID uint32,
	publicID string,
) (uint32, types.PublicID, error) {
	eventID, pub, standing, err := resolveEventStanding(ctx, deps, workspaceID, actorID, publicID)
	if err != nil {
		return 0, pub, err
	}
	if spec := eventVisibilityRefusal(standing); spec != nil {
		return 0, pub, httpErr(spec)
	}
	return eventID, pub, nil
}

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
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}

		// The link row is task-side, but it carries the event into the
		// task's linked-events list, which renders the event's title,
		// times and calendar name. Linking is therefore refused on an
		// event the actor cannot reach, at the same floor reading one
		// takes; whether it should additionally take the calendar's write
		// floor is a question about the link, not about disclosure.
		eventID, eventPub, err := resolveVisibleEvent(ctx, deps, ws.ID, actorID, in.Body.EventID)
		if err != nil {
			return nil, err
		}

		var linkPub types.PublicID
		if err := dbretry.InTx(ctx, deps.DB, "tasks.CreateTaskEventLink", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			var err error
			linkPub, _, err = itemkit.LinkTaskToEvent(ctx, tx, itemkit.LinkTaskToEventArgs{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				EventID:     eventID,
				Relation:    itemkit.Relation(in.Body.Relation),
				ActorUserID: actorID,
				SortWeight:  in.Body.SortWeight,
			})
			return err
		}); err != nil {
			return nil, translateItemkitTaskError(err)
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
//
// The link is resolved against the task named in the path, not against
// the workspace alone. A link id scoped only to a workspace is reachable
// by every member of it, so the task ACL the route already applies would
// guard a task the request then never touches: the caller passes the
// check on a task they may write and removes a link hanging off one they
// may not even read.
//
// The floor is that task ACL and nothing further. Removing a link does
// take a row off the event's linked-tasks list, which calendar members
// see — but the same is true of archiving the task, which no calendar
// rule guards either, and the relation is the task's half as much as the
// calendar's. Requiring a live grant on the event's calendar as well
// would leave a link permanently undeletable from the task side once the
// actor's membership on that calendar lapsed, stranding the task's own
// data behind a boundary that no longer concerns it. The read floor on
// [CreateTaskEventLink] is a different question: creating a link pulls
// the event's title, times and calendar name into a list the actor can
// read, and an unlink returns none of that.
func DeleteTaskEventLink(deps Deps) func(context.Context, *DeleteTaskEventLinkInput) (*DeleteTaskEventLinkOutput, error) {
	return func(ctx context.Context, in *DeleteTaskEventLinkInput) (*DeleteTaskEventLinkOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)

		linkPub, err := types.Parse(in.LinkID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// A link on another task is answered exactly as an unknown link
		// is: the caller learns nothing from the difference, and the id
		// is unguessable, so there is no set to enumerate either way.
		//
		// task_id is written once when the link is created and never
		// updated, so reading it before the transaction cannot go stale
		// in a direction that matters; a link disabled in between is
		// still refused by the unlink itself.
		link, err := deps.Queries.FindTaskEventLinkByPublicId(ctx, generated.FindTaskEventLinkByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    linkPub,
		})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, httpErr(apierrors.CalendarEventNotFound)
		case err != nil:
			return nil, httpErr(apierrors.InternalUnexpected)
		case link.TaskID != task.ID:
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}

		if err := dbretry.InTx(ctx, deps.DB, "tasks.DeleteTaskEventLink", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return itemkit.UnlinkTaskFromEvent(ctx, tx, itemkit.UnlinkTaskFromEventArgs{
				WorkspaceID: ws.ID,
				LinkID:      linkPub,
				ActorUserID: actorID,
			})
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarEventNotFound)
			}
			return nil, translateItemkitTaskError(err)
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
//
// Passing the task ACL reaches the links; it does not reach the
// calendars they point at, whose member lists are their own. The query
// decides that per row and answers with event_hidden, and a hidden row
// keeps its link fields and loses the event's. The alternative — leaving
// the row out — would tell the reader their task has fewer links than it
// has, and put total at odds with the list beneath it.
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
		// The calendar half of every row is decided against this actor, so
		// a request that carries no actor has no standing to evaluate and
		// is refused rather than answered against nobody.
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		rows, err := deps.Queries.ListLinkedEventsForTask(ctx, generated.ListLinkedEventsForTaskParams{
			ActorUserID: actorID,
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
				ID:         r.LinkPublicID.String(),
				Relation:   string(r.Relation),
				SortWeight: r.SortWeight,
				CreatedAt:  r.LinkCreatedAt.Unix(),
				// Both ids survive the redaction below: a UUID names a row
				// without describing it, and the routes that accept one
				// take the same membership floor this row was judged by.
				EventID:     r.EventPublicID.String(),
				CalendarID:  r.CalendarPublicID.String(),
				EventHidden: r.EventHidden,
			}
			// Everything the calendar owns is assigned here and nowhere
			// else, so a row the query marked hidden cannot pick any of it
			// up further down.
			if !r.EventHidden {
				link.EventTitle = r.EventTitle
				link.EventAllDay = r.AllDay
				link.EventTimezone = r.Timezone
				link.CalendarName = r.CalendarName
				link.EventStartAt = dbtype.UnixSecondsFromNullTime(r.StartAt)
				link.EventEndAt = dbtype.UnixSecondsFromNullTime(r.EndAt)
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
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		// An event id is reachable by every workspace member, guests
		// included, so the calendar the event lives on is the boundary
		// this answer sits behind: without the check a member holding no
		// grant on it could confirm that an id names a live event and
		// read the shape of its link set. A caller who cannot reach the
		// event is answered exactly as one who named a nonexistent id.
		_, eventPub, err := resolveVisibleEvent(ctx, deps, ws.ID, actorID, in.EventID)
		if err != nil {
			return nil, err
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		// Each link row carries the linked task's title, and calendar
		// membership says nothing about which tasks the actor may read.
		// The filter is what keeps a task the actor may not read out of
		// the answer; total counts the filtered set because COUNT(*)
		// OVER() sits inside the same statement.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		rows, err := deps.Queries.ListLinkedTasksForEvent(ctx, generated.ListLinkedTasksForEventParams{
			WorkspaceID:   ws.ID,
			EventPublicID: eventPub,
			Relation:      generated.TaskEventLinksRelation(in.Relation),
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			Limit:         limit,
			Offset:        in.Offset,
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
