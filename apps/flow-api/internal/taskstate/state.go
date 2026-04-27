// Package taskstate is the canonical write path for task derived_state
// transitions. Every code path that needs to move a task between
// derived_state values — the HTTP handler, the MCP tool, and the
// auto-action executor — funnels through [ApplyTransitionTx] so the
// state machine, the row lock, the derived_state UPDATE, and the
// `task.transition.<name>` event append all live in one place.
//
// derived_state itself is intentionally not writable through any other
// query (see sql/queries/crud.sql `UpdateTask` — the column is excluded
// from the SET list on purpose). This package preserves that invariant
// for callers that previously bypassed it with raw SQL.
package taskstate

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
)

// Transition names accepted by [NextState] / [ApplyTransitionTx]. The
// set mirrors the v1 state machine described in ADR 0001 and the public
// /tasks/{id}/transitions enum.
const (
	TransitionStart    = "start"
	TransitionBlock    = "block"
	TransitionUnblock  = "unblock"
	TransitionSubmit   = "submit"
	TransitionComplete = "complete"
	TransitionReopen   = "reopen"
	TransitionCancel   = "cancel"
)

// knownTransitions enumerates every transition name accepted by the
// state machine. Used by [IsKnownTransition].
var knownTransitions = map[string]struct{}{
	TransitionStart:    {},
	TransitionBlock:    {},
	TransitionUnblock:  {},
	TransitionSubmit:   {},
	TransitionComplete: {},
	TransitionReopen:   {},
	TransitionCancel:   {},
}

// IsKnownTransition reports whether the given name is one of the
// accepted transition verbs. Callers that need to surface
// WS.TASK.TRANSITION_UNKNOWN should check this before invoking
// [ApplyTransitionTx].
func IsKnownTransition(name string) bool {
	_, ok := knownTransitions[name]
	return ok
}

// NextState applies the v1 state machine described in ADR 0001 to a
// (current, transition) pair. It returns the next derived_state and
// true on success, or an empty string and false when the pair is
// illegal.
//
// The HTTP handler, MCP tool, and auto-action executor all rely on
// this single source of truth so the state machine has exactly one
// implementation in the binary.
func NextState(current generated.TasksDerivedState, transition string) (generated.TasksDerivedState, bool) {
	switch current {
	case generated.TasksDerivedStateOpen:
		switch transition {
		case TransitionStart:
			return generated.TasksDerivedStateWaiting, true
		case TransitionCancel:
			return generated.TasksDerivedStateCancelled, true
		case TransitionComplete:
			return generated.TasksDerivedStateDone, true
		}
	case generated.TasksDerivedStateWaiting:
		switch transition {
		case TransitionSubmit:
			return generated.TasksDerivedStateReview, true
		case TransitionBlock:
			return generated.TasksDerivedStateOpen, true
		case TransitionCancel:
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateReview:
		switch transition {
		case TransitionComplete:
			return generated.TasksDerivedStateDone, true
		case TransitionReopen:
			return generated.TasksDerivedStateWaiting, true
		case TransitionCancel:
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateDone:
		if transition == TransitionReopen {
			return generated.TasksDerivedStateWaiting, true
		}
	case generated.TasksDerivedStateCancelled:
		if transition == TransitionReopen {
			return generated.TasksDerivedStateOpen, true
		}
	}
	return "", false
}

// ApplyParams bundles the inputs to [ApplyTransitionTx]. TaskID is the
// internal numeric id (used for the FOR UPDATE lock); PublicID is the
// UUID v7 the persisted UPDATE keys on. Both are required because the
// generated TransitionTaskState query accepts (workspace_id, public_id)
// while the lock query accepts (id, workspace_id).
type ApplyParams struct {
	// WorkspaceID is the internal workspace id.
	WorkspaceID uint32
	// TaskID is the internal task id (events.task_id, FOR UPDATE lock).
	TaskID uint32
	// PublicID is the task's UUID v7. Used by the persisted UPDATE.
	PublicID types.PublicID
	// Transition is the verb to apply (see Transition* constants).
	Transition string
	// ActorUserID is the internal user id of the actor; nil when the
	// transition is driven by a system component (auto-action executor,
	// reconciler) rather than a real user.
	ActorUserID *int64
	// Reason is an optional human-readable note recorded on the event.
	Reason string
	// Via is a free-form tag stored on the event payload identifying
	// the originating subsystem ("api", "mcp", "auto_action"). Empty
	// strings are still accepted but the convention is to set it.
	Via string
	// ExtraPayload is merged into the event payload after the standard
	// keys (transition, fromState, toState, reason, via) are written.
	// Keys collide-by-overwrite; callers should avoid clashes.
	ExtraPayload map[string]any
}

// ApplyResult captures the before/after derived_state so the caller
// can echo the values into HTTP responses, audit logs, or further
// follow-up work without re-reading the row.
type ApplyResult struct {
	FromState generated.TasksDerivedState
	ToState   generated.TasksDerivedState
}

// ApplyTransitionTx is the canonical state-transition write path.
//
// The caller owns the transaction lifecycle (begin + commit/rollback);
// this function only:
//
//  1. acquires a FOR UPDATE row lock so concurrent transitions
//     serialize,
//  2. validates the requested transition against the v1 state machine,
//  3. persists the new derived_state via the sqlc query that is the
//     ONLY sanctioned writer of tasks.derived_state,
//  4. appends the canonical task.transition.<name> event in the same
//     transaction so the audit trail and the state change live and die
//     together.
//
// On failure the second return value is a non-nil [apierrors.Spec] that
// the caller can pass to handlerutil.HTTPErr to surface a structured
// problem+json envelope (or simply log when running outside the HTTP
// stack). The third return value is the underlying cause for log
// attribution; it is nil for validation failures (unknown / rejected
// transitions) where there is no wrapped Go error worth surfacing.
//
// Error semantics mirror the HTTP /transitions handler:
//   - [apierrors.WsTaskNotFound] when the row does not exist or is disabled
//   - [apierrors.WsTaskTransitionUnknown] for unknown verbs
//   - [apierrors.WsTaskTransitionRejected] for verbs the state machine refuses
//   - [apierrors.InternalUnexpected] otherwise
func ApplyTransitionTx(ctx context.Context, tx *sql.Tx, p ApplyParams) (ApplyResult, *apierrors.Spec, error) {
	if !IsKnownTransition(p.Transition) {
		return ApplyResult{}, apierrors.WsTaskTransitionUnknown, nil
	}

	qtx := generated.New(tx)

	locked, err := qtx.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
		ID:          p.TaskID,
		WorkspaceID: p.WorkspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return ApplyResult{}, apierrors.WsTaskNotFound, nil
		}
		return ApplyResult{}, apierrors.InternalUnexpected, err
	}

	next, ok := NextState(locked.DerivedState, p.Transition)
	if !ok {
		return ApplyResult{}, apierrors.WsTaskTransitionRejected, nil
	}

	// The trg_tasks_derived_state_guard BEFORE UPDATE trigger rejects
	// any mutation of derived_state unless this session variable is
	// set. Scope is the connection/tx; we clear it before returning so
	// the value never leaks to the next checkout of the pooled
	// connection.
	if _, err := tx.ExecContext(ctx, "SET @nf_derived_state_engine = 1"); err != nil {
		return ApplyResult{}, apierrors.InternalUnexpected, err
	}
	defer func() {
		_, _ = tx.ExecContext(ctx, "SET @nf_derived_state_engine = NULL")
	}()

	updatedBy := sql.NullInt32{}
	if p.ActorUserID != nil {
		updatedBy = sql.NullInt32{Int32: int32(*p.ActorUserID), Valid: true} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
	}
	if err := qtx.TransitionTaskState(ctx, generated.TransitionTaskStateParams{
		DerivedState:    next,
		Column2:         string(next),
		UpdatedByUserID: updatedBy,
		WorkspaceID:     p.WorkspaceID,
		PublicID:        p.PublicID,
	}); err != nil {
		return ApplyResult{}, apierrors.InternalUnexpected, err
	}

	taskInternal := int64(p.TaskID)
	payload := map[string]any{
		"taskId":     p.PublicID.String(),
		"transition": p.Transition,
		"fromState":  string(locked.DerivedState),
		"toState":    string(next),
		"reason":     p.Reason,
	}
	if p.Via != "" {
		payload["via"] = p.Via
	}
	for k, v := range p.ExtraPayload {
		payload[k] = v
	}

	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:        eventbus.TaskTransition(p.Transition),
		WorkspaceID: p.WorkspaceID,
		ActorUserID: p.ActorUserID,
		TaskID:      &taskInternal,
		Payload:     payload,
	}); err != nil {
		return ApplyResult{}, apierrors.InternalUnexpected, err
	}

	return ApplyResult{FromState: locked.DerivedState, ToState: next}, nil, nil
}
