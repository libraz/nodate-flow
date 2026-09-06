package tasks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
)

// shiftRefusal answers which refusal shifting an event on a calendar of
// the given standing earns, or nil when the shift may proceed.
//
// A shift moves the event's start time — and, on apply, the dates of the
// tasks linked to it — so it is a write to the calendar's contents rather
// than a read of the workspace, reached by naming the event rather than
// the calendar. That is exactly the question
// [calendars.EventPathWriteRefusalSpec] answers, so the rule is reached
// here rather than restated: an editor floor because a calendar's
// contents are visible to all of its members, a refusal at every role for
// a system calendar whose rows come from a provider feed, and the
// not-found an unknown event id gets for a caller who holds no membership
// at all. A shift route deciding for itself is how one calendar comes to
// answer differently depending on which surface asked — including which
// role it tells the caller to go and ask for.
//
// Only the standing crosses the boundary, converted here. Widening either
// type so the two could meet would put this package's resolver and the
// calendar package's rule in one shape, and the point of the split is
// that the rule does not depend on how the standing was resolved.
func shiftRefusal(standing *calendarStanding) *apierrors.Spec {
	if standing == nil {
		return calendars.EventPathWriteRefusalSpec(nil)
	}
	return calendars.EventPathWriteRefusalSpec(&calendars.CalendarStanding{
		Kind: standing.kind,
		Role: standing.role,
	})
}

// resolveShiftableEvent looks up the internal id of a calendar event by
// its public id within the workspace, and refuses unless the actor may
// move it.
//
// The lookup and the standing behind it come from
// [resolveEventStanding], shared with the read side so the two routes
// cannot come to disagree about which calendar an event lives on or what
// the actor holds there. What stays here is the decision: this route
// applies the write floor.
func resolveShiftableEvent(ctx context.Context, deps Deps, workspaceID, actorID uint32, publicID string) (uint32, error) {
	eventID, _, standing, err := resolveEventStanding(ctx, deps, workspaceID, actorID, publicID)
	if err != nil {
		return 0, err
	}
	if spec := shiftRefusal(standing); spec != nil {
		return 0, httpErr(spec)
	}
	return eventID, nil
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
		// The proposal names the tasks linked to the event and the other
		// events they contribute to, so it is answered only to someone who
		// may perform the shift it describes.
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}
		eventID, err := resolveShiftableEvent(ctx, deps, ws.ID, actorID, in.EventID)
		if err != nil {
			return nil, err
		}
		newStart := handlerutil.UnixToTime(in.Body.NewStartAt)

		var proposal itemkit.ShiftProposal
		if err := dbretry.InTx(ctx, deps.DB, "tasks.ProposeShift", &sql.TxOptions{ReadOnly: true},
			func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				proposal, err = itemkit.ProposeShiftEventAndChildren(ctx, tx, itemkit.ProposeShiftArgs{
					WorkspaceID: ws.ID,
					EventID:     eventID,
					NewStartAt:  newStart,
					ActorUserID: actorID,
					ActorRole:   acl.WorkspaceRole(ws.Role),
				})
				return err
			}); err != nil {
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
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.CalendarEventNotFound)
		}
		eventID, err := resolveShiftableEvent(ctx, deps, ws.ID, actorID, in.EventID)
		if err != nil {
			return nil, err
		}
		newStart := handlerutil.UnixToTime(in.Body.NewStartAt)

		confirmedInternal, err := resolveConfirmedTaskIDs(ctx, deps, ws.ID, in.Body.ConfirmedTaskIDs)
		if err != nil {
			return nil, err
		}

		// Capture the old start so we can compute the delta for the
		// response after itemkit runs.
		var oldStart sql.NullTime
		if err := dbretry.InTx(ctx, deps.DB, "tasks.ApplyShift", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if err := tx.QueryRowContext(ctx,
				`SELECT start_at FROM calendar_events WHERE id = ? AND workspace_id = ?`,
				eventID, ws.ID,
			).Scan(&oldStart); err != nil {
				return err
			}
			return itemkit.ApplyShiftEventAndChildren(ctx, tx, itemkit.ApplyShiftEventAndChildrenArgs{
				WorkspaceID:      ws.ID,
				EventID:          eventID,
				NewStartAt:       newStart,
				ConfirmedTaskIDs: confirmedInternal,
				ActorUserID:      actorID,
			})
		}); err != nil {
			return nil, translateItemkitTaskError(err)
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
