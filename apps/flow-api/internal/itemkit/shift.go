package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// OtherEventLink describes a contributes_to event OTHER than the
// umbrella being shifted. Surfaced so the caller can warn the user
// that a candidate task also contributes to a different event.
type OtherEventLink struct {
	EventID       uint32
	EventPublicID dbtype.PublicID
	EventTitle    string
	// EventStartAt is the linked event's current start_at. Zero when
	// the event is undated (planning bucket).
	EventStartAt time.Time
}

// ShiftCandidate is a task linked to the umbrella event via
// relation='contributes_to'.
type ShiftCandidate struct {
	TaskID       uint32
	TaskPublicID dbtype.PublicID
	TaskTitle    string
	// LinkID is the task_event_links row id between this task and the
	// umbrella event.
	LinkID uint32
	// OtherLinks is non-empty when the task is also linked to at least
	// one OTHER contributes_to event — it is a conflict candidate.
	OtherLinks []OtherEventLink
}

// ShiftProposal is the result of ProposeShiftEventAndChildren. The
// caller renders SafeTasks + ConflictTasks and sends confirmed task
// IDs back to ApplyShiftEventAndChildren.
type ShiftProposal struct {
	WorkspaceID   uint32
	EventID       uint32
	EventPublicID dbtype.PublicID
	OldStartAt    time.Time
	NewStartAt    time.Time
	// Delta is the signed time difference between NewStartAt and
	// OldStartAt. Tasks only move by the DATE component; this field
	// is included for UI labels.
	Delta         time.Duration
	SafeTasks     []ShiftCandidate
	ConflictTasks []ShiftCandidate
}

// ProposeShiftArgs names the umbrella event to analyse and the actor the
// analysis is answered to.
//
// The actor is part of the request rather than the caller's business
// because the proposal returns task titles: the set of tasks linked to an
// event and the set the actor may read are different sets, and only the
// second may be described back to them.
type ProposeShiftArgs struct {
	WorkspaceID uint32
	EventID     uint32
	NewStartAt  time.Time
	// ActorUserID is the internal id of the user the proposal is
	// answered to, and ActorRole their role in this workspace. Both feed
	// acl.TaskVisibilityFilter; neither may be zero.
	ActorUserID uint32
	ActorRole   acl.WorkspaceRole
}

// ProposeShiftEventAndChildren computes what shifting the umbrella
// event would do to its contributes_to-linked tasks. It does NOT
// mutate anything — purely a read-only analysis. The caller can
// present the breakdown (safe vs. conflict) and commit via
// ApplyShiftEventAndChildren once the user confirms which tasks to
// move along with the event.
func ProposeShiftEventAndChildren(ctx context.Context, tx TX, args ProposeShiftArgs) (ShiftProposal, error) {
	workspaceID, eventID, newStartAt := args.WorkspaceID, args.EventID, args.NewStartAt
	if workspaceID == 0 || eventID == 0 {
		return ShiftProposal{}, wrapInvariant("shift_ids_required", "workspaceID and eventID must be non-zero")
	}
	// The proposal names tasks reached by traversing task_event_links
	// from the event, so being allowed to shift the event says nothing
	// about being allowed to read the tasks hanging off it. Without an
	// actor there is no rule to apply and the titles would go out
	// unfiltered, so a missing one is refused rather than defaulted to
	// zero — a zero actor matches no membership row, which would answer
	// an empty proposal and read as "this event has no linked tasks".
	if args.ActorUserID == 0 {
		return ShiftProposal{}, wrapInvariant("shift_actor_required", "actor is required to resolve task visibility")
	}
	if !args.ActorRole.IsValid() {
		return ShiftProposal{}, wrapInvariant("shift_actor_role_required", "actor workspace role is required to resolve task visibility")
	}
	if newStartAt.IsZero() {
		return ShiftProposal{}, wrapInvariant("shift_target_required", "newStartAt must be non-zero")
	}
	evt, err := findEventByID(ctx, tx, workspaceID, eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ShiftProposal{}, fmt.Errorf("itemkit: event %d not found in ws %d", eventID, workspaceID)
		}
		return ShiftProposal{}, fmt.Errorf("itemkit: read event: %w", err)
	}
	if !evt.startAt.Valid {
		return ShiftProposal{}, wrapInvariant("shift_undated_event", "cannot shift an undated event; schedule it first")
	}

	proposal := ShiftProposal{
		WorkspaceID:   workspaceID,
		EventID:       evt.id,
		EventPublicID: evt.publicID,
		OldStartAt:    evt.startAt.Time,
		NewStartAt:    newStartAt,
		Delta:         newStartAt.Sub(evt.startAt.Time),
	}

	// The candidate list carries task titles into the response body, so
	// it is held to the Layer 4 rule the same way a task list is. The
	// join reads v_task_list_all rather than tasks so the shared
	// fragment can be spliced against the column names it is written
	// against instead of retyped for this alias; the view keeps the
	// enabled-row scope the tasks join had, and unlike v_task_list it
	// still admits an archived task, which can be linked to an event.
	//
	// A task filtered out here is also absent from the proposal the
	// caller confirms back, so a shift never moves a task its author
	// could not be shown.
	candidateWhere := []string{
		"tel.workspace_id = ?",
		"tel.event_id = ?",
		"tel.relation = 'contributes_to'",
		"tel.enabled = TRUE",
	}
	candidateArgs := []any{workspaceID, evt.id}
	if visFrag, visArgs := acl.TaskVisibilityFilter(args.ActorUserID, args.ActorRole); visFrag != "" {
		candidateWhere = append(candidateWhere, visFrag)
		candidateArgs = append(candidateArgs, visArgs...)
	}
	//#nosec G201 -- the only interpolated text is acl.TaskVisibilityFilter's own constant fragment; every value is bound.
	candidatesSQL := fmt.Sprintf(`
	  SELECT tel.id, v.task_internal_id, v.public_id, v.title
	    FROM task_event_links tel
	    INNER JOIN v_task_list_all v ON v.task_internal_id = tel.task_id
	   WHERE %s
	   ORDER BY tel.sort_weight ASC, tel.id ASC`, strings.Join(candidateWhere, " AND "))
	rows, err := tx.QueryContext(ctx, candidatesSQL, candidateArgs...)
	if err != nil {
		return ShiftProposal{}, fmt.Errorf("itemkit: list candidates: %w", err)
	}
	var candidates []ShiftCandidate
	for rows.Next() {
		var c ShiftCandidate
		if err := rows.Scan(&c.LinkID, &c.TaskID, &c.TaskPublicID, &c.TaskTitle); err != nil {
			rows.Close()
			return ShiftProposal{}, fmt.Errorf("itemkit: scan candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ShiftProposal{}, fmt.Errorf("itemkit: iterate candidates: %w", err)
	}
	rows.Close()

	const otherSQL = `
	  SELECT ce.id, ce.public_id, ce.title, ce.start_at
	    FROM task_event_links tel
	    INNER JOIN calendar_events ce ON ce.id = tel.event_id AND ce.enabled = TRUE
	   WHERE tel.workspace_id = ?
	     AND tel.task_id = ?
	     AND tel.event_id <> ?
	     AND tel.relation = 'contributes_to'
	     AND tel.enabled = TRUE
	   ORDER BY ce.start_at IS NULL, ce.start_at ASC, ce.id ASC`
	for i := range candidates {
		otherRows, err := tx.QueryContext(ctx, otherSQL, workspaceID, candidates[i].TaskID, evt.id)
		if err != nil {
			return ShiftProposal{}, fmt.Errorf("itemkit: list other links for task %d: %w", candidates[i].TaskID, err)
		}
		var others []OtherEventLink
		for otherRows.Next() {
			var o OtherEventLink
			var startAt sql.NullTime
			if err := otherRows.Scan(&o.EventID, &o.EventPublicID, &o.EventTitle, &startAt); err != nil {
				otherRows.Close()
				return ShiftProposal{}, fmt.Errorf("itemkit: scan other link: %w", err)
			}
			if startAt.Valid {
				o.EventStartAt = startAt.Time
			}
			others = append(others, o)
		}
		if err := otherRows.Err(); err != nil {
			otherRows.Close()
			return ShiftProposal{}, fmt.Errorf("itemkit: iterate other links: %w", err)
		}
		otherRows.Close()
		candidates[i].OtherLinks = others
	}

	for _, c := range candidates {
		if len(c.OtherLinks) > 0 {
			proposal.ConflictTasks = append(proposal.ConflictTasks, c)
		} else {
			proposal.SafeTasks = append(proposal.SafeTasks, c)
		}
	}
	return proposal, nil
}

// ApplyShiftEventAndChildrenArgs is the commit-side counterpart to
// ProposeShiftEventAndChildren.
//
// The caller sends the list of task internal ids the user has agreed
// to shift. The server re-derives the set of actually-linked
// candidates in the same tx and silently drops any id that no longer
// qualifies (defence against stale proposals).
type ApplyShiftEventAndChildrenArgs struct {
	WorkspaceID      uint32
	EventID          uint32
	NewStartAt       time.Time
	ConfirmedTaskIDs []uint32
	ActorUserID      uint32
	// Snap carries the actor's working-day preferences (passed through
	// to RescheduleEvent / RescheduleTask). Zero disables snap.
	Snap SnapConfig
}

// ApplyShiftEventAndChildren commits the shift of the umbrella event
// and the confirmed contributes_to-linked tasks in one transaction.
// Either everything moves or nothing does.
//
// Steps:
//  1. RescheduleEvent on the umbrella to NewStartAt / NewStartAt+oldDuration.
//  2. Compute a day-precision delta (tasks have DATE, not DATETIME).
//  3. For each confirmed task that is still contributes_to-linked,
//     shift its own due_on column by the day delta via RescheduleTask.
//     That call cascades into the task's own projection event
//     (RoleDue), not the umbrella.
func ApplyShiftEventAndChildren(ctx context.Context, tx TX, args ApplyShiftEventAndChildrenArgs) error {
	if args.WorkspaceID == 0 || args.EventID == 0 {
		return wrapInvariant("shift_ids_required", "workspaceID and eventID must be non-zero")
	}
	if args.NewStartAt.IsZero() {
		return wrapInvariant("shift_target_required", "newStartAt must be non-zero")
	}
	disarm, err := armProjectionGuard(ctx, tx)
	if err != nil {
		return err
	}
	defer disarm()

	evt, err := findEventByID(ctx, tx, args.WorkspaceID, args.EventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("itemkit: event %d not found in ws %d", args.EventID, args.WorkspaceID)
		}
		return fmt.Errorf("itemkit: read event: %w", err)
	}
	if !evt.startAt.Valid || !evt.endAt.Valid {
		return wrapInvariant("shift_undated_event", "cannot shift an undated event; schedule it first")
	}

	oldStart := evt.startAt.Time
	duration := evt.endAt.Time.Sub(oldStart)
	newEnd := args.NewStartAt.Add(duration)

	if err := RescheduleEvent(ctx, tx, RescheduleEventArgs{
		WorkspaceID: args.WorkspaceID,
		EventID:     evt.id,
		ActorUserID: args.ActorUserID,
		StartAt:     args.NewStartAt,
		EndAt:       newEnd,
		Snap:        args.Snap,
	}); err != nil {
		return err
	}

	if len(args.ConfirmedTaskIDs) == 0 {
		return nil
	}

	// How many days a move covers is a question about calendar dates, and
	// dates belong to the event's timezone. Measured in UTC, moving a
	// Tokyo meeting from 08:00 to 20:00 on the same day crosses a UTC
	// midnight and drags every linked task forward a day.
	z, err := region.Resolve(evt.timezone)
	if err != nil {
		return wrapInvariant("event_timezone_valid",
			fmt.Sprintf("event timezone %q is not a known IANA zone", evt.timezone))
	}
	dayDeltaInt := region.DayOf(args.NewStartAt, z).Sub(region.DayOf(oldStart, z))
	if dayDeltaInt == 0 {
		// Time-of-day only: tasks have DATE precision and do not move.
		return nil
	}

	confirmed := make(map[uint32]bool, len(args.ConfirmedTaskIDs))
	for _, id := range args.ConfirmedTaskIDs {
		if id != 0 {
			confirmed[id] = true
		}
	}

	const allowedSQL = `
	  SELECT task_id
	    FROM task_event_links
	   WHERE workspace_id = ?
	     AND event_id = ?
	     AND relation = 'contributes_to'
	     AND enabled = TRUE`
	rows, err := tx.QueryContext(ctx, allowedSQL, args.WorkspaceID, evt.id)
	if err != nil {
		return fmt.Errorf("itemkit: validate confirmed tasks: %w", err)
	}
	allowed := make(map[uint32]bool)
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("itemkit: scan allowed task: %w", err)
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("itemkit: iterate allowed tasks: %w", err)
	}
	rows.Close()

	for id := range confirmed {
		if !allowed[id] {
			continue
		}
		task, err := findTaskByID(ctx, tx, args.WorkspaceID, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("itemkit: read confirmed task %d: %w", id, err)
		}
		shiftArgs := RescheduleTaskArgs{
			WorkspaceID: args.WorkspaceID,
			TaskID:      task.id,
			ActorUserID: args.ActorUserID,
			Snap:        args.Snap,
		}
		if task.dueOn.Valid {
			shiftArgs.SetDueOn = true
			shiftArgs.DueOn = task.dueOn.Time.AddDate(0, 0, dayDeltaInt)
		}
		if !shiftArgs.SetDueOn {
			continue
		}
		if err := RescheduleTask(ctx, tx, shiftArgs); err != nil {
			return fmt.Errorf("itemkit: shift task %d: %w", id, err)
		}
	}
	return nil
}
