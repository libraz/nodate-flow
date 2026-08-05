package tasks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// resolveEventInWorkspace looks up the internal id of a calendar
// event by its public id, scoped to the given workspace. Returns
// apierrors.CalendarEventNotFound on miss.
func resolveEventInWorkspace(ctx context.Context, deps Deps, workspaceID uint32, publicID string) (uint32, error) {
	pub, err := types.Parse(publicID)
	if err != nil {
		return 0, httpErr(apierrors.CalendarEventNotFound)
	}
	var id uint32
	err = deps.DB.QueryRowContext(ctx,
		`SELECT id FROM calendar_events
		 WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
		 LIMIT 1`,
		workspaceID, pub,
	).Scan(&id)
	if err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarEventNotFound, apierrors.InternalUnexpected))
	}
	return id, nil
}

// ProposeShift handles POST /workspaces/{wsId}/calendar-events/{evtId}/propose-shift.
// Read-only analysis: returns tasks that contribute_to the umbrella
// event, partitioned into safe-to-shift (only linked here) and
// conflict (also linked to other events).
func ProposeShift(deps Deps) func(context.Context, *ProposeShiftInput) (*ProposeShiftOutput, error) {
	return func(ctx context.Context, in *ProposeShiftInput) (*ProposeShiftOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		eventID, err := resolveEventInWorkspace(ctx, deps, ws.ID, in.EventID)
		if err != nil {
			return nil, err
		}
		newStart := handlerutil.UnixToTime(in.Body.NewStartAt)

		tx, err := deps.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		proposal, err := itemkit.ProposeShiftEventAndChildren(ctx, tx, ws.ID, eventID, newStart)
		if err != nil {
			return nil, translateItemkitTaskError(err)
		}

		out := &ProposeShiftOutput{}
		out.Body.EventID = proposal.EventPublicID.String()
		out.Body.OldStartAt = proposal.OldStartAt.Unix()
		out.Body.NewStartAt = proposal.NewStartAt.Unix()
		out.Body.DeltaSeconds = int64(proposal.Delta / time.Second)
		out.Body.SafeTasks = shiftCandidatesToDTO(proposal.SafeTasks)
		out.Body.ConflictTasks = shiftCandidatesToDTO(proposal.ConflictTasks)
		return out, nil
	}
}

// ApplyShift handles POST /workspaces/{wsId}/calendar-events/{evtId}/apply-shift.
// Shifts the umbrella event by (newStartAt - oldStartAt) and moves
// each confirmed contributes_to-linked task's DATE columns by the
// same day-precision delta — all in one tx.
func ApplyShift(deps Deps) func(context.Context, *ApplyShiftInput) (*ApplyShiftOutput, error) {
	return func(ctx context.Context, in *ApplyShiftInput) (*ApplyShiftOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		eventID, err := resolveEventInWorkspace(ctx, deps, ws.ID, in.EventID)
		if err != nil {
			return nil, err
		}
		newStart := handlerutil.UnixToTime(in.Body.NewStartAt)

		confirmedInternal, err := resolveConfirmedTaskIDs(ctx, deps, ws.ID, in.Body.ConfirmedTaskIDs)
		if err != nil {
			return nil, err
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		// Capture the old start so we can compute the delta for the
		// response after itemkit runs.
		var oldStart sql.NullTime
		if err := tx.QueryRowContext(ctx,
			`SELECT start_at FROM calendar_events WHERE id = ? AND workspace_id = ?`,
			eventID, ws.ID,
		).Scan(&oldStart); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := itemkit.ApplyShiftEventAndChildren(ctx, tx, itemkit.ApplyShiftEventAndChildrenArgs{
			WorkspaceID:      ws.ID,
			EventID:          eventID,
			NewStartAt:       newStart,
			ConfirmedTaskIDs: confirmedInternal,
			ActorUserID:      actorID,
		}); err != nil {
			return nil, translateItemkitTaskError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "calendar_event.shift.apply",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "calendar_event",
				ResourceID:   in.EventID,
			})
		}

		var delta int64
		if oldStart.Valid {
			delta = newStart.Unix() - oldStart.Time.Unix()
		}
		out := &ApplyShiftOutput{}
		out.Body.Ok = true
		out.Body.ShiftedTasks = int32(len(confirmedInternal)) //#nosec G115 -- len of confirmed task ids, bounded by request payload size
		out.Body.DeltaSeconds = delta
		out.Body.NewStartAt = newStart.Unix()
		return out, nil
	}
}

// shiftCandidatesToDTO turns itemkit.ShiftCandidate slice into the
// wire DTO slice.
func shiftCandidatesToDTO(in []itemkit.ShiftCandidate) []ShiftCandidateDTO {
	out := make([]ShiftCandidateDTO, 0, len(in))
	for _, c := range in {
		dto := ShiftCandidateDTO{
			TaskID:    c.TaskPublicID.String(),
			TaskTitle: c.TaskTitle,
			// LinkID is internal-id; expose nothing sensitive. For UI
			// stability render it as a string; the frontend keys rows
			// by TaskID in practice.
			LinkID: "",
		}
		for _, o := range c.OtherLinks {
			link := OtherEventLinkDTO{
				EventID:    o.EventPublicID.String(),
				EventTitle: o.EventTitle,
			}
			if !o.EventStartAt.IsZero() {
				link.EventStartAt = int64Ptr(o.EventStartAt.Unix())
			}
			dto.OtherLinks = append(dto.OtherLinks, link)
		}
		out = append(out, dto)
	}
	return out
}

// resolveConfirmedTaskIDs translates public task IDs to internal ids
// within the workspace. Invalid / unknown ids are silently dropped;
// itemkit.ApplyShiftEventAndChildren revalidates the final set
// against the contributes_to-linked candidates in its own tx.
func resolveConfirmedTaskIDs(ctx context.Context, deps Deps, workspaceID uint32, publicIDs []string) ([]uint32, error) {
	if len(publicIDs) == 0 {
		return nil, nil
	}
	ids := make([]uint32, 0, len(publicIDs))
	for _, pid := range publicIDs {
		pub, err := types.Parse(pid)
		if err != nil {
			continue
		}
		var id uint32
		err = deps.DB.QueryRowContext(ctx,
			`SELECT id FROM tasks
			 WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
			 LIMIT 1`,
			workspaceID, pub,
		).Scan(&id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
