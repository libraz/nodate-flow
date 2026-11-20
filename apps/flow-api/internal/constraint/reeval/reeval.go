// Package reeval re-evaluates task constraints off the event bus. Every
// task-scoped event re-evaluates its task; the kinds that move a task's
// derived_state or enabled flag also re-evaluate the tasks depending on
// it, since their dependency facts read that state.
//
// Each task is evaluated in its own transaction. Delivery is at-most-once
// like every notify hook: a lost job is recovered by the task's next event
// or an explicit evaluate. Writes gated on a verdict (the completion gate)
// evaluate synchronously and do not rely on this subscriber.
package reeval

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Subscriber is the constraint re-evaluation notify hook.
type Subscriber struct {
	DB *sql.DB
	// Go runs job off the append path and reports whether it was
	// accepted. workspaceID lets a test harness track jobs per workspace.
	Go func(ctx context.Context, workspaceID uint32, job func(context.Context)) bool
}

// Hook returns the notify hook to register with [eventbus.AddNotifyHook].
func (s *Subscriber) Hook() eventbus.NotifyHook {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		// The engine's own output would otherwise re-trigger it.
		if isEngineOutput(eventType) {
			return
		}
		s.Go(ctx, workspaceID, func(ctx context.Context) {
			s.run(ctx, workspaceID, eventType, eventInternalID)
		})
	}
}

func (s *Subscriber) run(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	taskID, err := generated.New(s.DB).GetEventTaskForReevaluation(ctx, generated.GetEventTaskForReevaluationParams{
		ID:          eventInternalID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if !stderrors.Is(err, sql.ErrNoRows) {
			warn(ctx, "reeval: failed to read event task", eventType, err)
		}
		return
	}
	if !taskID.Valid {
		return
	}
	tid := uint32(taskID.Int32) //#nosec G115 -- events.task_id is tasks.id (INT UNSIGNED)
	if _, err := engine.Evaluate(ctx, s.DB, workspaceID, tid); err != nil {
		warn(ctx, "reeval: task evaluation failed", eventType, err)
	}
	if !triggersDependents(eventType) {
		return
	}
	dependents, err := engine.ListDependents(ctx, s.DB, workspaceID, tid)
	if err != nil {
		warn(ctx, "reeval: failed to list dependents", eventType, err)
		return
	}
	for _, dep := range dependents {
		if _, err := engine.Evaluate(ctx, s.DB, workspaceID, dep); err != nil {
			warn(ctx, "reeval: dependent evaluation failed", eventType, err)
		}
	}
}

// transitionPrefix is the dotted prefix of every task.transition.<name> kind.
var transitionPrefix = string(eventbus.TaskTransition(""))

// isEngineOutput reports whether eventType is a verdict event.
func isEngineOutput(eventType string) bool {
	switch eventbus.Kind(eventType) {
	case eventbus.TaskConstraintSatisfied, eventbus.TaskConstraintFailed:
		return true
	}
	return false
}

// triggersDependents reports whether eventType can change a task's
// derived_state or enabled flag, the only task fields a dependent's facts
// read. derived_state moves only through transitions (and the judge's
// auto-completion); enabled goes false only through deletion.
func triggersDependents(eventType string) bool {
	if strings.HasPrefix(eventType, transitionPrefix) {
		return true
	}
	switch eventbus.Kind(eventType) {
	case eventbus.TaskAutoCompleted, eventbus.ItemDeleted, eventbus.TaskDisabled:
		return true
	}
	return false
}

// warn logs a failed job. Internal ids stay out of logs; the event type and error identify the path.
func warn(ctx context.Context, msg, eventType string, err error) {
	slog.WarnContext(ctx, msg, slog.String("event_type", eventType), slog.String("err", err.Error()))
}
