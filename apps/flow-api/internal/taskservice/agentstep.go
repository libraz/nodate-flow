package taskservice

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// MaxStepTextRunes bounds the thought and the argument summary of a step.
const MaxStepTextRunes = 200

// StepRecord is one tool call of a run, as the timeline shows it.
type StepRecord struct {
	Tool    string
	Thought string
	// Action is the step's action; its body or title becomes the summary.
	Action  Action
	Outcome Outcome
}

// ErrStepOutcome is returned for an outcome outside the closed set.
var ErrStepOutcome = errors.New("taskservice: step outcome is not one of the known results")

func knownOutcome(o StepOutcome) bool {
	switch o {
	case StepApplied, StepProposed, StepNoop, StepSkipped, StepNotPermitted, StepInvalid, StepFailed, StepStopped:
		return true
	}
	return false
}

// argsSummary is the short text a step shows for its arguments.
func argsSummary(a Action) string {
	switch {
	case a.Body != "":
		return truncateRunes(a.Body, MaxStepTextRunes)
	case a.Title != "":
		return truncateRunes(a.Title, MaxStepTextRunes)
	}
	return ""
}

// RecordStep appends agent.task.step in its own transaction. It does not
// authorize, so the step that found the agent unassigned is still recorded;
// the task only has to exist in the agent's workspace.
func (s *Service) RecordStep(ctx context.Context, run RunPrincipal, task types.PublicID, step StepRef, rec StepRecord) error {
	if err := s.checkReadiness(true); err != nil {
		return err
	}
	if !validStepRef(step) {
		return ErrStepRef
	}
	if !knownOutcome(rec.Outcome.Result) {
		return ErrStepOutcome
	}
	ws := run.Agent.WorkspaceID()
	return dbretry.InTx(ctx, s.DB, "taskservice.RecordStep", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		t, err := acl.ResolveTaskByPublicID(ctx, tx.RawTx(), uuid.UUID(task.UUID()))
		if err != nil {
			return err
		}
		if t.WorkspaceID != ws {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		payload := map[string]any{
			"taskId":      task.String(),
			"runKey":      step.RunKey,
			"step":        step.Step,
			"tool":        rec.Tool,
			"thought":     truncateRunes(rec.Thought, MaxStepTextRunes),
			"argsSummary": argsSummary(rec.Action),
			"outcome":     string(rec.Outcome.Result),
		}
		if rec.Outcome.ProposalID != (types.PublicID{}) {
			payload["proposalId"] = rec.Outcome.ProposalID.String()
		}
		if rec.Outcome.ErrorCode != "" {
			payload["errorCode"] = rec.Outcome.ErrorCode
		}
		agentID := internalAgentID(run.Agent.AgentID())
		taskID := internalTaskID(t.ID)
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:         eventbus.AgentTaskStep,
			WorkspaceID:  ws,
			ActorAgentID: &agentID,
			TaskID:       &taskID,
			Payload:      payload,
		})
	})
}
