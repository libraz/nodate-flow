package taskservice

import (
	"context"
	"database/sql"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
)

// expectation is the stored state an approval applies against; a nil
// field is not checked.
type expectation struct {
	Priority *int32
	DueOn    *sql.NullTime
	State    *generated.TasksDerivedState
}

// applyArgs is everything apply needs. The caller has authorized the
// agent (and, for an approval, the approver) on the same transaction.
type applyArgs struct {
	WorkspaceID  uint32
	TaskID       uint32
	TaskPublicID types.PublicID
	ProjectID    uint32
	Visibility   generated.TasksVisibility
	Agent        execution.AgentIdentity
	Action       Action
	Expect       expectation
	// Extra is merged into the event payload; public ids only.
	Extra map[string]any
}

type failureKind int

const (
	failInvalid failureKind = iota + 1
	failConflict
	failRefused
)

// applyFailure is a write kit's refusal, classified so Execute and Decide
// can each map it onto their own result. Errors apply does not classify
// (database errors) are returned as they are so dbretry can retry them.
type applyFailure struct {
	kind  failureKind
	spec  *apierrors.Spec
	cause error
}

func (f *applyFailure) Error() string {
	if f.cause != nil {
		return "taskservice: apply refused: " + f.spec.Code + ": " + f.cause.Error()
	}
	return "taskservice: apply refused: " + f.spec.Code
}

func (f *applyFailure) Unwrap() error { return f.cause }

func (f *applyFailure) stepOutcome() Outcome {
	if f.kind == failInvalid {
		return outcome(StepInvalid, f.spec.Code)
	}
	return outcome(StepFailed, f.spec.Code)
}

func refused(kind failureKind, spec *apierrors.Spec, cause error) error {
	return &applyFailure{kind: kind, spec: spec, cause: cause}
}

// apply writes one agent action through the same kit a human edit uses,
// attributed to the agent, and registers the audit row for after the
// commit. It does not authorize.
func (s *Service) apply(ctx context.Context, tx *dbretry.Tx, args applyArgs) error {
	actor := mutationlog.Actor{AgentID: args.Agent.ID, WorkspaceID: args.WorkspaceID}
	a := args.Action
	switch a.Kind {
	case execution.ToolAddComment:
		return s.applyComment(ctx, tx, actor, args)
	case execution.ToolSetPriority:
		priority := a.Priority
		return s.applyUpdate(ctx, tx, actor, args, taskupdate.Patch{Priority: &priority, ExpectedPriority: args.Expect.Priority},
			map[string]any{"priority": priority})
	case execution.ToolSetDueOn:
		due := a.DueOn
		var shown any
		if due.Valid {
			shown = dateOnly(due)
		}
		return s.applyUpdate(ctx, tx, actor, args, taskupdate.Patch{DueOn: &due, ExpectedDueOn: args.Expect.DueOn},
			map[string]any{"dueOn": shown})
	case execution.ToolTransitionTask:
		return s.applyTransition(ctx, tx, actor, args)
	case execution.ToolCreateSubtask:
		return s.applySubtask(ctx, tx, actor, args)
	}
	return refused(failInvalid, apierrors.ValidationBodyFieldInvalid, nil)
}

func mergePayload(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func (s *Service) applyComment(ctx context.Context, tx *dbretry.Tx, actor mutationlog.Actor, args applyArgs) error {
	written, err := WriteComment(ctx, tx, WriteArgs{
		Actor:         actor,
		TaskID:        args.TaskID,
		TaskPublicID:  args.TaskPublicID,
		AuthorAgentID: sql.NullInt64{Int64: internalAgentID(args.Agent.ID), Valid: true},
		Body:          args.Action.Body,
		Payload: mergePayload(map[string]any{
			"authorKind":    AuthorKindAgent,
			"authorAgentId": args.Agent.PublicID.String(),
		}, args.Extra),
	})
	switch {
	case errors.Is(err, ErrCommentLength):
		return refused(failInvalid, apierrors.ValidationBodyFieldInvalid, err)
	case errors.Is(err, ErrCommentAgentUnavailable):
		return refused(failRefused, apierrors.AiAgentNotFound, err)
	case err != nil:
		return err
	}
	tx.AfterCommit(func() {
		s.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "comment.create",
			ResourceType: "comment",
			ResourceID:   written.PublicID.String(),
			Payload:      written.Payload,
			CallSite:     "taskservice.apply add_comment (WriteComment appended the event)",
		})
	})
	return nil
}

func (s *Service) applyUpdate(ctx context.Context, tx *dbretry.Tx, actor mutationlog.Actor, args applyArgs, patch taskupdate.Patch, change map[string]any) error {
	res, err := taskupdate.Apply(ctx, tx, taskupdate.Args{
		WorkspaceID:  args.WorkspaceID,
		TaskID:       args.TaskID,
		PublicID:     args.TaskPublicID,
		Patch:        patch,
		Actor:        actor,
		Via:          "agent",
		ExtraPayload: mergePayload(change, args.Extra),
	})
	if err != nil {
		return classifyUpdateError(err)
	}
	tx.AfterCommit(func() {
		s.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.update",
			ResourceType: "task",
			ResourceID:   args.TaskPublicID.String(),
			Payload:      res.Payload,
			CallSite:     "taskservice.apply (taskupdate.Apply appended the event)",
		})
	})
	return nil
}

// classifyUpdateError sorts a taskupdate.Apply refusal; anything else is
// returned unchanged.
func classifyUpdateError(err error) error {
	var ike *taskupdate.ItemkitError
	switch {
	case errors.Is(err, taskupdate.ErrPriorityConflict), errors.Is(err, taskupdate.ErrDueOnConflict):
		return refused(failConflict, apierrors.WsTaskUpdateConflict, err)
	case errors.Is(err, taskupdate.ErrTaskNotFound):
		return refused(failRefused, apierrors.WsTaskNotFound, err)
	case errors.As(err, &ike):
		return refused(failRefused, itemkitSpec(ike.Err), err)
	}
	switch taskrules.Classify(err) {
	case taskrules.ViolationTitleEmpty:
		return refused(failInvalid, apierrors.ValidationBodyFieldInvalid, err)
	case taskrules.ViolationDueBeforeStart:
		return refused(failInvalid, apierrors.ValidationBodyDueBeforeStart, err)
	}
	return err
}

// itemkitSpec names a calendar-mirror refusal with the code the task
// routes answer with.
func itemkitSpec(err error) *apierrors.Spec {
	switch handlerutil.ClassifyItemkitError(err) {
	case handlerutil.ItemkitErrorNotFound:
		return apierrors.WsTaskNotFound
	case handlerutil.ItemkitErrorRecurrenceWithLink:
		return apierrors.ItemItemkitRecurrenceWithTaskLink
	case handlerutil.ItemkitErrorInvariant:
		return apierrors.ItemItemkitInvariantViolation
	}
	return apierrors.InternalUnexpected
}

func (s *Service) applyTransition(ctx context.Context, tx *dbretry.Tx, actor mutationlog.Actor, args applyArgs) error {
	agentID := internalAgentID(args.Agent.ID)
	res, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
		WorkspaceID:   args.WorkspaceID,
		TaskID:        args.TaskID,
		PublicID:      args.TaskPublicID,
		Transition:    args.Action.Transition,
		ExpectedState: args.Expect.State,
		ActorAgentID:  &agentID,
		Via:           "agent",
		ExtraPayload:  args.Extra,
	})
	switch {
	case err != nil:
		return err
	case spec == apierrors.WsTaskUpdateConflict:
		return refused(failConflict, spec, nil)
	case spec != nil:
		return refused(failRefused, spec, nil)
	}
	tx.AfterCommit(func() {
		s.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.transition",
			ResourceType: "task",
			ResourceID:   args.TaskPublicID.String(),
			Payload: mergePayload(map[string]any{
				"taskId":     args.TaskPublicID.String(),
				"transition": args.Action.Transition,
				"fromState":  string(res.FromState),
				"toState":    string(res.ToState),
				"via":        "agent",
			}, args.Extra),
			CallSite: "taskservice.apply transition_task (taskstate appended the event)",
		})
	})
	return nil
}

// applySubtask creates a child task in the parent's project and audience.
// The caller took the project lock as its transaction's first statement.
func (s *Service) applySubtask(ctx context.Context, tx *dbretry.Tx, actor mutationlog.Actor, args applyArgs) error {
	title, err := taskrules.NewTitle(args.Action.Title)
	if err != nil {
		return refused(failInvalid, apierrors.ValidationBodyFieldInvalid, err)
	}
	created, err := taskcreate.New(ctx, tx, taskcreate.Unattributed(), taskcreate.Args{
		WorkspaceID:  args.WorkspaceID,
		ProjectID:    args.ProjectID,
		ParentTaskID: nullInt32(args.TaskID),
		Title:        title,
		Visibility:   args.Visibility,
	})
	if err != nil {
		if taskrules.Classify(err) != taskrules.ViolationOther {
			return refused(failInvalid, apierrors.ValidationBodyFieldInvalid, err)
		}
		return err
	}
	return s.Mutations.RecordInTx(ctx, tx, actor, mutationlog.Mutation{
		EventType:    eventbus.TaskCreated,
		AuditAction:  "task.create",
		ResourceType: "task",
		ResourceID:   created.PublicID.String(),
		TaskID:       &created.ID,
		Payload: mergePayload(map[string]any{
			"taskId":       created.PublicID.String(),
			"title":        title.String(),
			"parentTaskId": args.TaskPublicID.String(),
			"via":          "agent",
		}, args.Extra),
		CallSite: "taskservice.apply create_subtask",
	})
}
