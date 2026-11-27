package taskservice

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// Limits the service enforces on one agent step. The per-run limits
// (calls, writes, subtasks per run) belong to the task-work loop.
const (
	MaxSubtaskTitleRunes = 500
	MaxAgentChildren     = 20
	MaxPendingProposals  = 10
	ProposalLifetime     = 7 * 24 * time.Hour
)

// StepOutcome is the closed set of results of one agent step.
type StepOutcome string

const (
	StepApplied      StepOutcome = "applied"
	StepProposed     StepOutcome = "proposed"
	StepNoop         StepOutcome = "noop"
	StepSkipped      StepOutcome = "skipped"
	StepNotPermitted StepOutcome = "not_permitted"
	StepInvalid      StepOutcome = "invalid"
	StepFailed       StepOutcome = "failed"
	StepStopped      StepOutcome = "stopped"
)

// Reasons carried in Outcome.ErrorCode for results decided by a limit
// rather than by an API error.
const (
	SkipDuplicate          = "duplicate"
	SkipPendingCap         = "pending_cap"
	NotPermittedSubtaskCap = "subtask_cap"
)

var (
	// ErrStepRef is returned when a step reference cannot be stored.
	ErrStepRef = errors.New("taskservice: step reference needs a run key of at most 36 characters and a step within 0..65535")
	// ErrSubtaskProject is returned when a create_subtask action carries no project.
	ErrSubtaskProject = errors.New("taskservice: create_subtask needs the project id read with the task")
)

// StepRef names one step of one agent run.
type StepRef struct {
	RunKey string
	Step   int
}

// RunPrincipal is the authority one agent run acts under. Agent is built
// with scopes [write:workspace], the grant keys as tools, the task as its
// only target and no delegator; the delegator and the trigger user are
// authorized separately as human principals (see Execute).
type RunPrincipal struct {
	Agent execution.Principal
	// DelegatorID is the assignment's delegated_by_user_id; zero for none.
	DelegatorID uint32
	// TriggerUserID is the actor_user_id of the event that started the run.
	TriggerUserID uint32
}

// Action is one operation an agent chose. Only the fields its Kind uses
// are read.
type Action struct {
	Kind     execution.Tool
	Body     string
	Priority int32
	// DueOn with Valid=false clears the due date.
	DueOn      sql.NullTime
	Transition string
	Title      string
	// ProjectID is the task's internal project id, required for create_subtask.
	ProjectID uint32
}

// Outcome is the result of Execute.
type Outcome struct {
	Result     StepOutcome
	ProposalID types.PublicID
	ErrorCode  string
}

// agentTransitions are the transitions an agent may apply or propose.
var agentTransitions = map[string]struct{}{
	taskstate.TransitionStart:    {},
	taskstate.TransitionBlock:    {},
	taskstate.TransitionUnblock:  {},
	taskstate.TransitionSubmit:   {},
	taskstate.TransitionComplete: {},
}

// stepRollback carries a non-committing outcome out of the transaction
// closure so dbretry rolls the attempt back.
type stepRollback struct{ outcome Outcome }

func (r *stepRollback) Error() string {
	return "taskservice: step outcome " + string(r.outcome.Result)
}

func outcome(result StepOutcome, code string) Outcome {
	return Outcome{Result: result, ErrorCode: code}
}

func isAgentTool(t execution.Tool) bool {
	switch t {
	case execution.ToolAddComment, execution.ToolSetPriority, execution.ToolSetDueOn,
		execution.ToolTransitionTask, execution.ToolCreateSubtask:
		return true
	}
	return false
}

func validStepRef(step StepRef) bool {
	return step.RunKey != "" && len(step.RunKey) <= 36 && step.Step >= 0 && step.Step <= 65535
}

// Execute authorizes and carries out one agent step in one transaction.
// It commits only when the step is applied or proposed; every other
// outcome rolls the attempt back. The error return is reserved for
// failures that are not step outcomes (database errors, caller bugs).
func (s *Service) Execute(ctx context.Context, run RunPrincipal, task types.PublicID, a Action, step StepRef) (Outcome, error) {
	if err := s.checkReadiness(true); err != nil {
		return Outcome{}, err
	}
	if !validStepRef(step) {
		return Outcome{}, ErrStepRef
	}
	if !isAgentTool(a.Kind) {
		return outcome(StepInvalid, apierrors.ValidationBodyFieldInvalid.Code), nil
	}
	if a.Kind == execution.ToolCreateSubtask && a.ProjectID == 0 {
		return Outcome{}, ErrSubtaskProject
	}
	var out Outcome
	err := dbretry.InTx(ctx, s.DB, "taskservice.Execute", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		o, err := s.executeTx(ctx, tx, run, task, a, step)
		if err != nil {
			return err
		}
		out = o
		if o.Result != StepApplied && o.Result != StepProposed {
			return &stepRollback{outcome: o}
		}
		return nil
	})
	var rb *stepRollback
	if errors.As(err, &rb) {
		return rb.outcome, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func (s *Service) executeTx(ctx context.Context, tx *dbretry.Tx, run RunPrincipal, target types.PublicID, a Action, step StepRef) (Outcome, error) {
	ws := run.Agent.WorkspaceID()
	capability := a.Kind.Capability()
	if a.Kind == execution.ToolCreateSubtask {
		// The project lock must be the first statement (taskcreate.LockProject).
		if err := taskcreate.LockProject(ctx, tx, ws, a.ProjectID); err != nil {
			return Outcome{}, err
		}
	}
	authz, err := execution.AuthorizeTaskTx(ctx, tx, run.Agent, target, capability)
	if err != nil {
		return agentAuthorizationOutcome(err)
	}
	if a.Kind == execution.ToolCreateSubtask && authz.Task.ProjectID != a.ProjectID {
		return outcome(StepStopped, apierrors.WsTaskUpdateConflict.Code), nil
	}
	qtx := s.Queries.WithTx(tx.RawTx())
	current, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{WorkspaceID: ws, PublicID: target})
	if errors.Is(err, sql.ErrNoRows) {
		return outcome(StepStopped, apierrors.WsTaskNotFound.Code), nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if !taskWorkable(current) {
		return outcome(StepStopped, apierrors.WsTaskAgentTaskNotWorkable.Code), nil
	}
	if a.Kind == execution.ToolSetDueOn {
		a.DueOn = dateOf(a.DueOn)
	}
	if o, done, err := s.checkAction(ctx, qtx, ws, authz.Task.ID, current, a); err != nil || done {
		return o, err
	}

	auto := authz.Mode == execution.GrantModeAuto
	if auto {
		if auto, err = s.humansAllow(ctx, tx, run, target, capability); err != nil {
			return Outcome{}, err
		}
	}
	if !auto {
		return s.propose(ctx, tx, run, authz, current, a, step)
	}
	state := current.DerivedState
	err = s.apply(ctx, tx, applyArgs{
		WorkspaceID:  ws,
		TaskID:       authz.Task.ID,
		TaskPublicID: target,
		ProjectID:    authz.Task.ProjectID,
		Visibility:   current.Visibility,
		Agent:        *authz.Agent,
		Action:       a,
		Expect:       expectation{State: &state},
		Extra:        map[string]any{"runKey": step.RunKey},
	})
	var fail *applyFailure
	if errors.As(err, &fail) {
		return fail.stepOutcome(), nil
	}
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Result: StepApplied}, nil
}

// agentAuthorizationOutcome maps an agent authorization refusal onto a
// step outcome. Errors that are not refusals are returned for retry.
func agentAuthorizationOutcome(err error) (Outcome, error) {
	var apiErr *apierrors.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Spec == apierrors.WsTaskNotFound:
		return outcome(StepStopped, apiErr.Spec.Code), nil
	case errors.As(err, &apiErr) && apiErr.Spec == apierrors.McpScopeInsufficient:
		return outcome(StepNotPermitted, ""), nil
	case errors.As(err, &apiErr):
		return outcome(StepFailed, apiErr.Spec.Code), nil
	case errors.Is(err, execution.ErrTaskOutsideWorkspace):
		return outcome(StepFailed, apierrors.WsTaskNotFound.Code), nil
	}
	return Outcome{}, err
}

// taskWorkable reports whether an agent may still act on the task.
func taskWorkable(t generated.FindTaskByPublicIdRow) bool {
	return t.Visibility == generated.TasksVisibilityPublic &&
		t.DerivedState != generated.TasksDerivedStateDone &&
		t.DerivedState != generated.TasksDerivedStateCancelled &&
		!t.ArchivedAt.Valid
}

// checkAction validates the arguments against the current task. done is
// true when the step ends here (invalid, noop or not_permitted).
func (s *Service) checkAction(ctx context.Context, qtx *generated.Queries, ws, taskID uint32, current generated.FindTaskByPublicIdRow, a Action) (Outcome, bool, error) {
	invalid := func(spec *apierrors.Spec) (Outcome, bool, error) {
		return outcome(StepInvalid, spec.Code), true, nil
	}
	switch a.Kind {
	case execution.ToolAddComment:
		if !utf8.ValidString(a.Body) || requireCommentBody(a.Body) != nil {
			return invalid(apierrors.ValidationBodyFieldInvalid)
		}
	case execution.ToolSetPriority:
		if a.Priority < MinPriority || a.Priority > MaxPriority {
			return invalid(apierrors.ValidationBodyFieldInvalid)
		}
		if a.Priority == current.Priority {
			return outcome(StepNoop, ""), true, nil
		}
	case execution.ToolSetDueOn:
		if taskrules.DateOrder(a.DueOn, current.StartedOn) != nil {
			return invalid(apierrors.ValidationBodyDueBeforeStart)
		}
		if sameDate(current.DueOn, a.DueOn) {
			return outcome(StepNoop, ""), true, nil
		}
	case execution.ToolTransitionTask:
		if _, ok := agentTransitions[a.Transition]; !ok {
			return invalid(apierrors.ValidationBodyFieldInvalid)
		}
		settings, err := qtx.GetTaskWorkflowSettings(ctx, generated.GetTaskWorkflowSettingsParams{WorkspaceID: ws, ID: taskID})
		if err != nil {
			return Outcome{}, true, err
		}
		switch err := taskstate.CheckTransition(current.DerivedState, settings.CompletionPolicy, a.Transition); {
		case errors.Is(err, taskstate.ErrTransitionUnknown):
			return invalid(apierrors.WsTaskTransitionUnknown)
		case errors.Is(err, taskstate.ErrTransitionRejected):
			return invalid(apierrors.WsTaskTransitionRejected)
		case errors.Is(err, taskstate.ErrCompletionNeedsApproval):
			return invalid(apierrors.WsTaskCompletionNeedsApproval)
		case err != nil:
			return Outcome{}, true, err
		}
	case execution.ToolCreateSubtask:
		if !utf8.ValidString(a.Title) || utf8.RuneCountInString(a.Title) > MaxSubtaskTitleRunes {
			return invalid(apierrors.ValidationBodyFieldInvalid)
		}
		if _, err := taskrules.NewTitle(a.Title); err != nil {
			return invalid(apierrors.ValidationBodyFieldInvalid)
		}
		n, err := qtx.CountAgentCreatedChildren(ctx, generated.CountAgentCreatedChildrenParams{
			WorkspaceID:  ws,
			ParentTaskID: nullInt32(taskID),
		})
		if err != nil {
			return Outcome{}, true, err
		}
		if n >= MaxAgentChildren {
			return outcome(StepNotPermitted, NotPermittedSubtaskCap), true, nil
		}
	}
	return Outcome{}, false, nil
}

// humansAllow reports whether both the delegator and the trigger user can
// perform the capability themselves, checked in the step's transaction.
// A missing delegator or trigger user downgrades the step to a proposal.
func (s *Service) humansAllow(ctx context.Context, tx *dbretry.Tx, run RunPrincipal, target types.PublicID, capability execution.Capability) (bool, error) {
	if run.DelegatorID == 0 || run.TriggerUserID == 0 {
		return false, nil
	}
	ok, err := userCan(ctx, tx, run.Agent.WorkspaceID(), run.DelegatorID, target, capability)
	if err != nil || !ok || run.TriggerUserID == run.DelegatorID {
		return ok, err
	}
	return userCan(ctx, tx, run.Agent.WorkspaceID(), run.TriggerUserID, target, capability)
}

func userCan(ctx context.Context, tx *dbretry.Tx, ws, userID uint32, target types.PublicID, capability execution.Capability) (bool, error) {
	principal, err := execution.NewUserPrincipal(ws, userID)
	if err != nil {
		return false, err
	}
	_, err = execution.AuthorizeTaskTx(ctx, tx, principal, target, capability)
	var apiErr *apierrors.APIError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &apiErr), errors.Is(err, execution.ErrTaskOutsideWorkspace):
		return false, nil
	}
	return false, err
}

// propose records the step as a pending proposal unless an identical one
// is pending or the agent's pending cap is reached.
func (s *Service) propose(ctx context.Context, tx *dbretry.Tx, run RunPrincipal, authz execution.TaskAuthorization, current generated.FindTaskByPublicIdRow, a Action, step StepRef) (Outcome, error) {
	ws := run.Agent.WorkspaceID()
	qtx := s.Queries.WithTx(tx.RawTx())
	args, err := encodeProposalArgs(a)
	if err != nil {
		return Outcome{}, err
	}
	now := time.Now().UTC()
	kind := generated.AgentActionProposalsKind(a.Kind)
	_, err = qtx.FindPendingDuplicateProposal(ctx, generated.FindPendingDuplicateProposalParams{
		WorkspaceID: ws,
		TaskID:      authz.Task.ID,
		AgentID:     authz.Agent.ID,
		Now:         now,
		Kind:        kind,
		ArgsJson:    args,
	})
	if err == nil {
		return outcome(StepSkipped, SkipDuplicate), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, err
	}
	pending, err := qtx.CountPendingProposalsForTaskAgent(ctx, generated.CountPendingProposalsForTaskAgentParams{
		WorkspaceID: ws,
		TaskID:      authz.Task.ID,
		AgentID:     authz.Agent.ID,
		Now:         now,
	})
	if err != nil {
		return Outcome{}, err
	}
	if pending >= MaxPendingProposals {
		return outcome(StepSkipped, SkipPendingCap), nil
	}
	pre, err := s.preconditionFor(ctx, qtx, ws, authz.Task.ID, current, a.Kind)
	if err != nil {
		return Outcome{}, err
	}
	pub := types.New()
	_, err = qtx.InsertAgentActionProposal(ctx, generated.InsertAgentActionProposalParams{
		PublicID:          pub,
		WorkspaceID:       ws,
		TaskID:            authz.Task.ID,
		AgentID:           authz.Agent.ID,
		DelegatedByUserID: optionalUser(run.DelegatorID),
		TriggerUserID:     optionalUser(run.TriggerUserID),
		RunKey:            step.RunKey,
		Step:              uint16(step.Step), //#nosec G115 -- validStepRef bounds the step to 0..65535
		Kind:              kind,
		ArgsJson:          args,
		PreconditionJson:  pre,
		ExpiresAt:         now.Add(ProposalLifetime),
	})
	if apierr.IsDuplicateEntry(err) {
		return outcome(StepSkipped, SkipDuplicate), nil
	}
	if err != nil {
		return Outcome{}, err
	}
	payload := map[string]any{
		"taskId":     current.PublicID.String(),
		"proposalId": pub.String(),
		"kind":       string(a.Kind),
		"agentId":    authz.Agent.PublicID.String(),
		"runKey":     step.RunKey,
		"step":       step.Step,
	}
	if run.DelegatorID != 0 {
		delegator, err := qtx.LockCommentHumanAuthor(ctx, run.DelegatorID)
		switch {
		case err == nil:
			payload["targetUserPublicId"] = delegator.PublicID.String()
		case !errors.Is(err, sql.ErrNoRows):
			return Outcome{}, err
		}
	}
	agentID := internalAgentID(authz.Agent.ID)
	taskID := internalTaskID(authz.Task.ID)
	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:         eventbus.AgentActionProposed,
		WorkspaceID:  ws,
		ActorAgentID: &agentID,
		TaskID:       &taskID,
		Payload:      payload,
	}); err != nil {
		return Outcome{}, err
	}
	return Outcome{Result: StepProposed, ProposalID: pub}, nil
}

// transitionEventKinds are every task.transition.* kind, for the latest
// transition id a transition proposal is checked against.
func transitionEventKinds() []string {
	names := []string{
		taskstate.TransitionStart, taskstate.TransitionBlock, taskstate.TransitionUnblock,
		taskstate.TransitionSubmit, taskstate.TransitionComplete, taskstate.TransitionReopen,
		taskstate.TransitionCancel,
	}
	kinds := make([]string, len(names))
	for i, name := range names {
		kinds[i] = string(eventbus.TaskTransition(name))
	}
	return kinds
}

func latestTransitionEventID(ctx context.Context, qtx *generated.Queries, ws, taskID uint32) (int64, error) {
	return qtx.GetLatestTransitionEventID(ctx, generated.GetLatestTransitionEventIDParams{
		WorkspaceID:     ws,
		TaskID:          nullInt32(taskID),
		TransitionTypes: transitionEventKinds(),
	})
}

// dateOf drops the time of day so a due date compares and stores by day.
func dateOf(v sql.NullTime) sql.NullTime {
	if !v.Valid {
		return sql.NullTime{}
	}
	y, m, d := v.Time.Date()
	return sql.NullTime{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func sameDate(a, b sql.NullTime) bool {
	return dateOnly(a) == dateOnly(b)
}

func nullInt32(id uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- INT UNSIGNED ids fit int32 within realistic deployments
}

func optionalUser(id uint32) sql.NullInt32 {
	if id == 0 {
		return sql.NullInt32{}
	}
	return nullInt32(id)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
