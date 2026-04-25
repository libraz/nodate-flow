package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
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

// ProposeShiftEventAndChildren computes what shifting the umbrella
// event would do to its contributes_to-linked tasks. It does NOT
// mutate anything — purely a read-only analysis. The caller can
// present the breakdown (safe vs. conflict) and commit via
// ApplyShiftEventAndChildren once the user confirms which tasks to
// move along with the event.
func ProposeShiftEventAndChildren(ctx context.Context, tx TX, workspaceID, eventID uint32, newStartAt time.Time) (ShiftProposal, error) {
	if workspaceID == 0 || eventID == 0 {
		return ShiftProposal{}, wrapInvariant("shift_ids_required", "workspaceID and eventID must be non-zero")
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

	const candidatesSQL = `
	  SELECT tel.id, t.id, t.public_id, t.title
	    FROM task_event_links tel
	    INNER JOIN tasks t ON t.id = tel.task_id AND t.enabled = TRUE
	   WHERE tel.workspace_id = ?
	     AND tel.event_id = ?
	     AND tel.relation = 'contributes_to'
	     AND tel.enabled = TRUE
	   ORDER BY tel.sort_weight ASC, tel.id ASC`
	rows, err := tx.QueryContext(ctx, candidatesSQL, workspaceID, evt.id)
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

	dayDelta := dateOnly(args.NewStartAt).Sub(dateOnly(oldStart)).Hours() / 24
	dayDeltaInt := int(dayDelta)
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
