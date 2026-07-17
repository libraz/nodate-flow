package events

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/taskstate"
)

// reverseStateRollback maps the original event's type onto the state
// machine transition that walks derived_state back to its pre-event
// value. Today only `task.auto_completed` semantically mutates a
// task's derived_state in production wiring (the Applier emits the
// event and — once production swaps in a real TaskMutator — calls
// CompleteTask). The reversal must therefore also walk the task out
// of `done` via the canonical `reopen` transition; using
// taskstate.ApplyTransitionTx keeps the row-lock, the trigger guard,
// and the audit event aligned with the engine's invariants
// (CLAUDE.md rule 10: derived_state is only mutated through the
// engine path).
//
// Empty value means "no state rollback for this event type" — the
// compensating event is still appended (so timeline UIs can render
// the undo) but nothing else moves.
var reverseStateRollback = map[string]string{
	eventbus.TaskAutoCompleted: taskstate.TransitionReopen,
}

// Reverse handles POST /workspaces/{wsId}/events/{eventPublicId}/reverse.
// Behaviour is documented on the package; in short:
//
//  1. Workspace membership is enforced by RequireWorkspaceMember middleware.
//  2. Resolve the target event via queries.FindEventForReverse. A miss
//     returns AI.REVERSE.TARGET_NOT_FOUND (404) regardless of whether
//     the row exists in some other workspace.
//  3. Eligibility: actor_agent_id must be set AND actor_user_id +
//     actor_system_source must both be unset. Anything else returns
//     AI.REVERSE.NOT_LLM_ORIGIN (403). The compound check is symmetric
//     with the FindEventForReverse projection — three independent
//     columns must agree.
//  4. Idempotency: a row that is already the target of an enabled
//     reverse returns AI.REVERSE.ALREADY_REVERSED (409). The plan
//     scopes reversal to a single compensating event per origin. The
//     UNIQUE (workspace_id, reverses_event_id) index on `events`
//     backs this check at the storage layer: two concurrent reverses
//     of the same target can both pass the WasReversed read, but only
//     one compensating INSERT commits — the loser's INSERT fails with
//     ER_DUP_ENTRY and is mapped to the same 409 (see step 5).
//  5. The compensating event is appended via eventbus.AppendReverseEvent
//     (the dedicated entry point that bypasses the judge-kind guard;
//     see the helper's docstring for the rationale). It re-uses the
//     original event's type so the projection cancels both rows out
//     symmetrically. actor_user_id is the reverser (the user driving
//     the undo), actor_agent_id is NULL, triggered_by_signal_id is
//     NULL (the reversal itself has no signal lineage).
//  6. For event types listed in [reverseStateRollback] the handler
//     additionally drives the task back through the canonical state
//     transition (e.g. TaskAutoCompleted → reopen). This is the
//     state-cancellation half of the J5 plan: rather than mutate
//     derived_state directly (forbidden by CLAUDE.md rule 10) we
//     funnel through taskstate.ApplyTransitionTx so the engine's
//     row-lock + trigger guard + transition event all run together.
//
// On success the response is 201 with the new compensating event's
// public_id + occurred_at (unix seconds per CLAUDE.md rule 17).
func Reverse(deps Deps) func(context.Context, *ReverseInput) (*ReverseOutput, error) {
	return func(ctx context.Context, in *ReverseInput) (*ReverseOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			// RequireWorkspaceMember should have rejected this; defence
			// in depth so a routing mistake cannot expose the reversal
			// surface without an authenticated workspace context.
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		actorInternal, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		eventPub, err := types.Parse(in.EventPublicID)
		if err != nil {
			// A malformed UUID v7 cannot match any row; surface the same
			// 404 as a real miss so cross-workspace probes cannot
			// distinguish the two failure modes.
			return nil, httpErr(apierrors.AiReverseTargetNotFound)
		}

		target, err := deps.Queries.FindEventForReverse(ctx, generated.FindEventForReverseParams{
			PublicID:    eventPub,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AiReverseTargetNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Eligibility check. actor_agent_id MUST be set, and neither of
		// the user / system-source columns may be populated — otherwise
		// the row is not LLM-origin and the user-facing undo flow refuses
		// to touch it (per the AI_REVERSE_NOT_LLM_ORIGIN spec).
		if !target.ActorAgentID.Valid || target.ActorUserID.Valid || target.ActorSystemSource.Valid {
			return nil, httpErr(apierrors.AiReverseNotLlmOrigin)
		}
		if target.WasReversed {
			return nil, httpErr(apierrors.AiReverseAlreadyReversed)
		}

		// Resolve the reverser's public_id once. The payload carries
		// the user public id (not the internal one) so the timeline UI
		// can render "Reversed by <displayName>" without a join.
		reverserPub, err := deps.Queries.FindUserPublicIdById(ctx, actorInternal)
		if err != nil {
			// A missing reverser row would be an invariant violation
			// (the request authenticated successfully). Log and bail
			// with INTERNAL.UNEXPECTED rather than leaking detail.
			slog.ErrorContext(ctx, "events.Reverse: FindUserPublicIdById failed",
				slog.Any("err", err),
				slog.Uint64("workspace_internal", uint64(ws.ID)),
				slog.Uint64("user_internal", uint64(actorInternal)),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Open a single transaction so the compensating event INSERT
		// and the optional state-rollback transition land together.
		// Both writers use the same *sql.Tx so eventbus.Append's
		// retry-loop branch is short-circuited (the tx owns the retry
		// boundary) and the trigger-guard session variable set by
		// ApplyTransitionTx is scoped to this connection only.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		// Optional state rollback. Runs BEFORE the compensating event
		// append so a failed rollback cannot leave a half-applied
		// reversal on the timeline (the tx rolls back atomically).
		if transition, needsRollback := reverseStateRollback[target.Type]; needsRollback {
			if err := applyStateRollback(ctx, tx, ws.ID, eventPub, transition, actorInternal); err != nil {
				return nil, err
			}
		}

		// Append the compensating event. Type is preserved (the
		// projection / UI cancels matching reversed_event_id pairs out
		// symmetrically), the reverser is the user, and the payload
		// carries enough lineage for the timeline renderer.
		actor := int64(actorInternal)    //#nosec G115 -- actor user id from session, fits int64
		origInternal := int64(target.ID) //#nosec G115 -- event ids are auto-increment DB ids and fit int64.
		result, err := eventbus.AppendReverseEvent(ctx, tx, eventbus.Event{
			Type:            target.Type,
			WorkspaceID:     ws.ID,
			ActorUserID:     &actor,
			ReversesEventID: &origInternal,
			Payload: map[string]any{
				"reversed_event_public_id":   eventPub.String(),
				"reversed_by_user_public_id": reverserPub.String(),
			},
		})
		if err != nil {
			if handlerutil.IsDuplicateEntry(err) {
				// A concurrent reverse of the same target committed its
				// compensating row between our FindEventForReverse read
				// and this INSERT; the UNIQUE (workspace_id,
				// reverses_event_id) index rejected ours. The event is
				// already reversed, so surface the same canonical 409 as
				// the WasReversed pre-check instead of a 500. The tx
				// (including any state rollback we attempted) is rolled
				// back by the deferred Rollback — the winner's rollback
				// transition is the one that stands. public_id is UUID
				// v7, so realistically the only unique key this INSERT
				// can violate is the reverses index.
				return nil, httpErr(apierrors.AiReverseAlreadyReversed)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &ReverseOutput{
			Status: 201,
			Body: ReverseOutputBody{
				PublicID:   result.PublicID.String(),
				OccurredAt: result.OccurredAt.Unix(),
			},
		}, nil
	}
}

// applyStateRollback walks the task referenced by the target event
// back through the canonical state transition (e.g. `reopen` for a
// reversed `TaskAutoCompleted`). The task is resolved fresh inside
// the transaction via the event row's task_id so we do not have to
// thread it through FindEventForReverse — the projection already
// scopes by workspace.
//
// A state-machine rejection (transition illegal for the current
// derived_state, e.g. the task was already manually reopened by a
// concurrent user) is silently absorbed: the compensating event is
// the canonical record of the reversal, and skipping a no-op
// transition keeps the response code predictable. The transition is
// best-effort because the events row is immutable — once the
// timeline shows the reverse, the user expectation is that the undo
// landed; an unrelated state mismatch does not invalidate that
// record.
func applyStateRollback(ctx context.Context, tx *sql.Tx, wsID uint32, eventPublicID types.PublicID, transition string, actorInternal uint32) error {
	// Re-fetch the task pointer from the event row using the same tx
	// so the lock-order with the upcoming ApplyTransitionTx FOR UPDATE
	// stays consistent. We deliberately query the event again instead
	// of plumbing task_id through FindEventForReverse so the helper
	// stays narrowly typed.
	const q = `SELECT t.id, t.public_id
		FROM events e
		LEFT JOIN tasks t ON t.id = e.task_id AND t.enabled = TRUE
		WHERE e.workspace_id = ? AND e.public_id = ? AND e.enabled = TRUE LIMIT 1`
	var (
		taskID    sql.NullInt32
		taskPubID types.PublicID
	)
	if err := tx.QueryRowContext(ctx, q, wsID, eventPublicID).Scan(&taskID, &taskPubID); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			// Event vanished between FindEventForReverse and now? That
			// would be an invariant violation; surface as INTERNAL to
			// preserve the 404 vs. 500 distinction.
			return httpErr(apierrors.InternalUnexpected)
		}
		return httpErr(apierrors.InternalUnexpected)
	}
	if !taskID.Valid {
		// Event has no associated task — there is nothing to walk
		// back. The compensating event is still appended by the
		// caller; we just skip the transition.
		return nil
	}

	actor := int64(actorInternal)
	_, spec, applyErr := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
		WorkspaceID: wsID,
		TaskID:      uint32(taskID.Int32), //#nosec G115 -- tasks.id fits uint32 within realistic deployments
		PublicID:    taskPubID,
		Transition:  transition,
		ActorUserID: &actor,
		Reason:      "reverse",
		Via:         "api.reverse",
		ExtraPayload: map[string]any{
			"reversed_event_public_id": eventPublicID.String(),
		},
	})
	if applyErr != nil {
		// Underlying DB failure; bubble up so the tx rolls back.
		return httpErr(apierrors.InternalUnexpected)
	}
	if spec != nil {
		switch spec.Code {
		case apierrors.WsTaskTransitionRejected.Code:
			// The task is no longer in a state the rollback can leave;
			// likely a concurrent reopen already moved it. The
			// compensating event is still the canonical record of the
			// reversal, so we accept the no-op.
			slog.WarnContext(ctx, "events.Reverse: state rollback skipped (transition rejected)",
				slog.String("transition", transition),
				slog.Uint64("workspace_internal", uint64(wsID)),
			)
			return nil
		default:
			return httpErr(spec)
		}
	}
	return nil
}
