// POST /tasks/{id}/agents/{agentId}/run — ask an assigned agent to work on
// the task now. The endpoint only records agent.task.run_requested; the
// agent runtime picks the event up like any other trigger.

package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// maxTaskAgentRoster bounds the agent actors read to find the assignee.
const maxTaskAgentRoster = 200

// RunAgentInput is the request for POST /tasks/{id}/agents/{agentId}/run.
type RunAgentInput struct {
	ID      string `path:"id"`
	AgentID string `path:"agentId" doc:"AI agent public id (UUID v7)"`
}

// RunAgentBody acknowledges a recorded run request.
type RunAgentBody struct {
	RunRequestedAt int64 `json:"runRequestedAt" doc:"Unix seconds when the request was recorded"`
}

// RunAgentOutput is the response for POST /tasks/{id}/agents/{agentId}/run.
type RunAgentOutput struct {
	Body RunAgentBody
}

// RunAgent handles POST /tasks/{id}/agents/{agentId}/run. It checks what the
// runtime would check before it starts, so a request that could not run is
// refused rather than acknowledged.
func RunAgent(deps Deps) func(context.Context, *RunAgentInput) (*RunAgentOutput, error) {
	return func(ctx context.Context, in *RunAgentInput) (*RunAgentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		agentPub, err := types.Parse(in.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		agent, err := deps.Queries.GetAgentKindByPublicID(ctx, generated.GetAgentKindByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    agentPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AiAgentNotFound, apierrors.InternalUnexpected))
		}
		if !agent.Enabled {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}

		assigned, err := isAssignee(ctx, deps, ws.ID, types.FromUUID(task.PublicID), agentPub)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if !assigned {
			return nil, httpErr(apierrors.WsTaskAgentNotAssigned)
		}
		if agent.Paused {
			return nil, httpErr(apierrors.AiAgentPaused)
		}
		if !hasGrants(agent.ToolsJson) {
			return nil, httpErr(apierrors.WsTaskAgentNoGrants)
		}

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		if !taskAcceptsAgentWork(row) {
			return nil, httpErr(apierrors.WsTaskAgentTaskNotWorkable)
		}

		if capHit, err := ai.CheckBudget(ctx, deps.BudgetGuard, ws.ID); err != nil {
			if capHit {
				return nil, httpErr(apierrors.AiCostGuardExceeded)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		now := time.Now().UTC()
		if err := ai.CheckAgentCostCap(ctx, deps.Queries, agent.ID, now); err != nil {
			if errors.Is(err, ai.ErrAgentCostCapExceeded) {
				return nil, httpErr(apierrors.WsTaskAgentCostCapExceeded)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskInternal := int64(task.ID)
		if err := dbretry.InTx(ctx, deps.DB, "tasks.RunAgent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.AgentTaskRunRequested,
				AuditAction:  "ai_agent.run.request",
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				TaskID:       &taskInternal,
				Payload: map[string]any{
					"taskId":  task.PublicID.String(),
					"agentId": agentPub.String(),
				},
				CallSite: "tasks.RunAgent",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &RunAgentOutput{Body: RunAgentBody{RunRequestedAt: now.Unix()}}, nil
	}
}

// isAssignee reports whether the agent holds an enabled assignee row on the
// task.
func isAssignee(ctx context.Context, deps Deps, workspaceID uint32, taskPub, agentPub types.PublicID) (bool, error) {
	rows, err := deps.Queries.ListAgentActorsForTask(ctx, generated.ListAgentActorsForTaskParams{
		WorkspaceID: workspaceID,
		PublicID:    taskPub,
		Limit:       maxTaskAgentRoster,
	})
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.AgentPublicID == agentPub && r.Role == generated.TaskActorsRoleAssignee {
			return true, nil
		}
	}
	return false, nil
}

// taskAcceptsAgentWork mirrors the runtime's condition on the task: public,
// unfinished and unarchived.
func taskAcceptsAgentWork(t generated.FindTaskByPublicIdRow) bool {
	return t.Visibility == generated.TasksVisibilityPublic &&
		t.DerivedState != generated.TasksDerivedStateDone &&
		t.DerivedState != generated.TasksDerivedStateCancelled &&
		!t.ArchivedAt.Valid
}
