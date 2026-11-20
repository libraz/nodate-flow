package tasks

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// Transition handles POST /tasks/{id}/transitions. It applies a single
// state machine step, persists the new derived_state, and appends a
// `task.transition.<name>` event in the same transaction.
//
// All of that work is delegated to [taskstate.ApplyTransitionTx], the
// single canonical write path for tasks.derived_state. The MCP tool and
// the auto-action executor go through the same helper.
//
// The transaction is wrapped in [dbretry.InTx] so an InnoDB deadlock
// (ER_LOCK_DEADLOCK 1213) on the FK record locks acquired by
// taskstate.Apply (workspaces / tasks / users) restarts in a fresh
// transaction instead of bubbling up as a 500. Concurrent state
// transitions on the same workspace are the dominant deadlock source
// under heavy parallel test load and in production agent fan-out.
func Transition(deps Deps) func(context.Context, *TransitionTaskInput) (*TransitionTaskOutput, error) {
	return func(ctx context.Context, in *TransitionTaskInput) (*TransitionTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		extra := map[string]any{}
		if in.Body.OccurredAt != 0 {
			extra["occurredAt"] = in.Body.OccurredAt
		}

		var (
			result        taskstate.ApplyResult
			specErr       *apierrors.Spec
			replayed      bool
			actorID       uint32
			auditMutation mutationlog.Mutation
			output        TransitionTaskOutput
		)
		actorID, ok = middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthTokenMissingOrMalformed)
		}
		var expectedState *generated.TasksDerivedState
		if in.Body.ExpectedState != "" {
			value := generated.TasksDerivedState(in.Body.ExpectedState)
			expectedState = &value
		}
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Transition", taskMutationTxOptions(in.IdempotencyKey), func(ctx context.Context, tx *dbretry.Tx) error {
			result = taskstate.ApplyResult{}
			specErr = nil
			replayed = false
			receipt, isReplay, err := lockTaskMutation(ctx, tx, ws, task, actorID,
				execution.CapabilityEditTask, "task.transition", in.IdempotencyKey, in.Body, &output.Body)
			if err != nil {
				return err
			}
			if isReplay {
				replayed = true
				return nil
			}

			r, spec, applyErr := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
				WorkspaceID:   ws.ID,
				TaskID:        task.ID,
				PublicID:      types.FromUUID(task.PublicID),
				Transition:    in.Body.Transition,
				ExpectedState: expectedState,
				ActorUserID:   actorPtr(ctx),
				Reason:        in.Body.Reason,
				Via:           "api",
				ExtraPayload:  extra,
			})
			// taskstate returns an apierrors.Spec for validation
			// failures (with applyErr == nil) and for SQL-level errors
			// (with applyErr != nil and spec == InternalUnexpected).
			// Only the former should short-circuit the retry loop;
			// the latter must propagate the raw error so dbretry can
			// recognise transient deadlocks.
			if applyErr != nil {
				return applyErr
			}
			if spec != nil {
				specErr = spec
				return errSpec
			}
			result = r
			auditMutation = mutationlog.Mutation{
				AuditAction:  "task.transition",
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				Payload: map[string]any{
					"transition": in.Body.Transition,
					"fromState":  string(result.FromState),
					"toState":    string(result.ToState),
				},
				CallSite: "tasks.Transition",
			}
			if in.IdempotencyKey == "" {
				return nil
			}
			row, err := deps.Queries.WithTx(tx.RawTx()).FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if err != nil {
				return err
			}
			output.Body = rowToTaskFromFind(row)
			return completeTaskMutation(ctx, tx, receipt, output.Body)
		})
		if specErr != nil {
			return nil, httpErr(specErr)
		}
		if txErr != nil {
			return nil, taskMutationHTTPError(txErr)
		}
		if replayed {
			return &output, nil
		}
		deps.Mutations.RecordTxAudit(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, auditMutation)
		if in.IdempotencyKey == "" {
			row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			output.Body = rowToTaskFromFind(row)
		}
		return &output, nil
	}
}

// errSpec is the sentinel returned from the dbretry.InTx callback when
// taskstate.ApplyTransitionTx surfaces an apierrors.Spec (validation
// failure). It is non-transient by construction so dbretry.Do skips
// the retry loop and returns it verbatim; the outer Transition
// handler then unwraps the captured spec.
var errSpec = errSpecValue{}

type errSpecValue struct{}

func (errSpecValue) Error() string { return "tasks: transition spec validation failed" }
