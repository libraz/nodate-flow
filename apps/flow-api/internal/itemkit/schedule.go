package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// ScheduleTaskArgs carries everything needed to place or move a
// projection-style calendar event for a task. For RoleDue the
// operation is idempotent: calling twice with the same (task, role)
// updates the existing link in place. For RoleScheduled every call
// creates an additional time-block event.
type ScheduleTaskArgs struct {
	WorkspaceID uint32
	TaskID      uint32
	CalendarID  uint32
	ActorUserID uint32
	Role        DateRole

	Title    string // event title; usually set to task.title by callers
	StartAt  time.Time
	EndAt    time.Time
	AllDay   bool
	Timezone string
	// Visibility is a calendar_events.visibility enum value
	// ("default" / "public" / "private" / "confidential"). Empty
	// defaults to "default".
	Visibility string
	// ShowAs is the calendar_events.show_as enum value; empty defaults
	// to "busy".
	ShowAs string

	// Snap carries the actor's working-day preferences. Zero value
	// disables snap behavior; see SnapConfig for semantics.
	Snap SnapConfig
}

// ScheduleTask links (or updates the link between) a task and a
// projection calendar event. Returns the event's public_id and
// internal id.
//
// For RoleDue, the call is idempotent: re-invoking with the same
// (task_id, role) pair moves the existing event. For RoleScheduled
// every invocation creates a new event — callers that want
// idempotent scheduled blocks must pass the existing event through
// RescheduleEvent.
//
// ScheduleTask also updates tasks.due_on to mirror the projection
// date so the task's own DATE column remains in sync.
func ScheduleTask(ctx context.Context, tx TX, args ScheduleTaskArgs) (dbtype.PublicID, uint32, error) {
	if !args.Role.IsValid() {
		return dbtype.PublicID{}, 0, wrapInvariant("role_valid", fmt.Sprintf("unknown role %q", args.Role))
	}
	if args.Timezone == "" {
		args.Timezone = "UTC"
	}
	if args.Visibility == "" {
		args.Visibility = "default"
	}
	if args.ShowAs == "" {
		args.ShowAs = "busy"
	}

	if args.StartAt.IsZero() || args.EndAt.IsZero() {
		return dbtype.PublicID{}, 0, wrapInvariant("start_end_required", "start_at and end_at are required for projection links")
	}
	// Ordered through the rule both transports answer with, so the same
	// inverted pair is refused whether it arrived from a browser or an
	// agent. The refusal leaves here as an itemkit invariant because each
	// transport already owns how it renders one.
	if calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(args.StartAt), calendarrules.UnixSeconds(args.EndAt)) != nil {
		return dbtype.PublicID{}, 0, wrapInvariant("chronology", "end_at before start_at")
	}

	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return dbtype.PublicID{}, 0, err
	}
	defer disarm()

	snap := applySnap(args.StartAt, args.EndAt, args.Snap)
	args.StartAt = snap.NewStart
	args.EndAt = snap.NewEnd
	// An all-day projection is a date, and the stored instant that stands
	// for it has to be the same one whichever writer produced it. Snap runs
	// first because it moves the whole window by a whole number of days in
	// the actor's zone, so pinning afterwards is what lands on midnight.
	args.StartAt, args.EndAt = allDayWindow(args.AllDay, args.StartAt, args.EndAt)

	task, err := findTaskByID(ctx, tx, args.WorkspaceID, args.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: task %d not found in ws %d", args.TaskID, args.WorkspaceID)
		}
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: read task: %w", err)
	}

	if args.Role == RoleDue {
		existing, err := findLinkedEvent(ctx, tx, task.id, args.Role)
		switch {
		case err == nil:
			return reschedulePutExisting(ctx, tx, task, existing, args, snap)
		case !errors.Is(err, sql.ErrNoRows):
			return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: find linked event: %w", err)
		}
	}

	title := args.Title
	if title == "" {
		title = task.title
	}
	eventPublicID := dbtype.New()

	const insertSQL = `INSERT INTO calendar_events
	    (public_id, workspace_id, calendar_id, kind, visibility, show_as,
	     title, all_day, start_at, end_at, timezone,
	     owner_user_id, created_by_user_id, task_id, task_role)
	    VALUES (?, ?, ?, 'event', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := tx.ExecContext(ctx, insertSQL,
		eventPublicID, args.WorkspaceID, args.CalendarID,
		args.Visibility, args.ShowAs,
		title, args.AllDay,
		sql.NullTime{Time: args.StartAt, Valid: true},
		sql.NullTime{Time: args.EndAt, Valid: true},
		args.Timezone,
		args.ActorUserID, args.ActorUserID,
		args.TaskID, string(args.Role),
	)
	if err != nil {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: insert event: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: last insert id: %w", err)
	}
	eventID := uint32(id64) //#nosec G115 -- LastInsertId for calendar_events.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

	if err := applySnapFlags(ctx, tx, eventID, snap); err != nil {
		return dbtype.PublicID{}, 0, err
	}

	if err := propagateTaskDateFromRole(ctx, tx, task.id, args.Role, args.StartAt, args.Timezone, args.AllDay); err != nil {
		return dbtype.PublicID{}, 0, err
	}

	if err := appendItemEvents(ctx, tx, eventbus.ItemScheduled, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId":  task.publicID.String(),
			"eventPublicId": eventPublicID.String(),
			"role":          string(args.Role),
		}); err != nil {
		return dbtype.PublicID{}, 0, err
	}
	return eventPublicID, eventID, nil
}

// reschedulePutExisting updates an already-linked event in place
// rather than creating a duplicate. Called from ScheduleTask when it
// finds a pre-existing role link.
func reschedulePutExisting(ctx context.Context, tx TX, task taskRow, existing eventRow, args ScheduleTaskArgs, snap snapOutcome) (dbtype.PublicID, uint32, error) {
	title := args.Title
	if title == "" {
		title = task.title
	}
	const updateSQL = `UPDATE calendar_events SET
	    calendar_id = ?,
	    visibility = ?,
	    show_as = ?,
	    title = ?,
	    all_day = ?,
	    start_at = ?,
	    end_at = ?,
	    timezone = ?
	    WHERE id = ? AND workspace_id = ?`
	if _, err := tx.ExecContext(ctx, updateSQL,
		args.CalendarID, args.Visibility, args.ShowAs, title, args.AllDay,
		sql.NullTime{Time: args.StartAt, Valid: true},
		sql.NullTime{Time: args.EndAt, Valid: true},
		args.Timezone,
		existing.id, args.WorkspaceID,
	); err != nil {
		return dbtype.PublicID{}, 0, fmt.Errorf("itemkit: update linked event: %w", err)
	}

	if err := applySnapFlags(ctx, tx, existing.id, snap); err != nil {
		return dbtype.PublicID{}, 0, err
	}

	if err := propagateTaskDateFromRole(ctx, tx, task.id, args.Role, args.StartAt, args.Timezone, args.AllDay); err != nil {
		return dbtype.PublicID{}, 0, err
	}

	if err := appendItemEvents(ctx, tx, eventbus.ItemRescheduled, args.WorkspaceID, &args.ActorUserID, &task.id,
		map[string]any{
			"taskPublicId":  task.publicID.String(),
			"eventPublicId": existing.publicID.String(),
			"role":          string(args.Role),
		}); err != nil {
		return dbtype.PublicID{}, 0, err
	}
	return existing.publicID, existing.id, nil
}

// propagateTaskDateFromRole writes tasks.due_on from the scheduled
// event's start date. Has no effect for RoleScheduled.
//
// Which date that is depends on whether the row is all-day; see
// [projectionDate]. For a timed row the event's timezone decides, because
// an 08:00 meeting in Asia/Tokyo is on the 11th for everyone who attends
// it even though the instant stored in UTC falls on the 10th. Deriving
// the date from the UTC instant moved the deadline a day earlier than the
// meeting that set it, and every downstream reader — task list, Gantt,
// public lens, `time.due_before` evaluation — inherited the shift.
//
// The date is held to [taskrules.DateOrder] against the started_on the
// task already carries. Nothing in the database refuses a due date
// earlier than the start date, so a projection dragged behind the task's
// own start would store the inversion and leave a task nobody can act on
// — and the caller moved an event, so no field-level refusal upstream is
// looking at this pair at all. The read is by primary key inside the
// caller's transaction, so the value it orders against is the one this
// statement is about to sit beside.
func propagateTaskDateFromRole(ctx context.Context, tx TX, taskID uint32, role DateRole, startAt time.Time, tz string, allDay bool) error {
	if startAt.IsZero() || role != RoleDue {
		return nil
	}
	date, err := projectionDate(startAt, tz, allDay)
	if err != nil {
		return err
	}
	startedOn, err := taskStartedOn(ctx, tx, taskID)
	if err != nil {
		return err
	}
	due := sql.NullTime{Time: date.DateColumn(), Valid: true}
	if taskrules.Classify(taskrules.DateOrder(due, startedOn)) == taskrules.ViolationDueBeforeStart {
		return wrapInvariant("date_order",
			"projected due date precedes the task's start date")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET due_on = ? WHERE id = ?", date, taskID); err != nil {
		return fmt.Errorf("itemkit: propagate task due_on: %w", err)
	}
	return nil
}

// taskStartedOn reads the start date the ordering rule holds a projected
// due date against. A task with no start date scans as an invalid
// sql.NullTime, which the rule reads as nothing to order.
func taskStartedOn(ctx context.Context, tx TX, taskID uint32) (sql.NullTime, error) {
	var startedOn sql.NullTime
	if err := tx.QueryRowContext(ctx,
		"SELECT started_on FROM tasks WHERE id = ?", taskID).Scan(&startedOn); err != nil {
		return sql.NullTime{}, fmt.Errorf("itemkit: read task started_on: %w", err)
	}
	return startedOn, nil
}

// appendItemEvents appends the new item.* kind plus (for dual-emit
// transition) the legacy task.* / calendar.event.* kind so existing
// webhook/notification/SSE subscribers keep working for one release.
func appendItemEvents(
	ctx context.Context,
	tx TX,
	kind eventbus.Kind,
	workspaceID uint32,
	actorUserID *uint32,
	taskID *uint32,
	payload map[string]any,
) error {
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        kind,
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		TaskID:      taskID,
		Payload:     payload,
	}); err != nil {
		return fmt.Errorf("itemkit: append item event: %w", err)
	}
	legacy := legacyKindFor(kind)
	if legacy == "" {
		return nil
	}
	if _, err := eventlog.Append(ctx, tx, eventlog.Event{
		Type:        legacy,
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		TaskID:      taskID,
		Payload:     payload,
	}); err != nil {
		return fmt.Errorf("itemkit: append legacy event: %w", err)
	}
	return nil
}

// legacyKindFor returns the legacy kind to dual-emit alongside a new
// item.* kind. Empty return skips dual-emit.
func legacyKindFor(itemKind eventbus.Kind) eventbus.Kind {
	switch itemKind {
	case eventbus.ItemScheduled:
		return eventbus.CalEventCreated
	case eventbus.ItemUnscheduled:
		return eventbus.CalEventDeleted
	case eventbus.ItemRescheduled, eventbus.ItemRenamed:
		return eventbus.CalEventUpdated
	case eventbus.ItemDeleted:
		return eventbus.TaskDisabled
	}
	return ""
}
