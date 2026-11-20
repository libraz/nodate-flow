// AI agent assignment on tasks via task_actors (kind='agent').
// DELETE reuses RemoveActor since task_actors rows are addressed by
// public_id regardless of kind.

package tasks

import (
	"context"

	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// AddAgentActor handles POST /tasks/{id}/agents. Attaches an AI agent
// to the task as an actor (assignee/reviewer/...).
func AddAgentActor(deps Deps) func(context.Context, *AddTaskAgentActorInput) (*AddTaskAgentActorOutput, error) {
	return func(ctx context.Context, in *AddTaskAgentActorInput) (*AddTaskAgentActorOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		agentPub, err := types.Parse(in.Body.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}
		agentID, err := deps.Queries.FindAgentIDByPublicIDForWorkspace(ctx, generated.FindAgentIDByPublicIDForWorkspaceParams{
			WorkspaceID: ws.ID,
			PublicID:    agentPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AiProviderNotConfigured, apierrors.InternalUnexpected))
		}
		role, perr := parseActorRole(in.Body.Role)
		if perr != nil {
			return nil, translateActorRoleError(perr)
		}
		pub := types.New()
		actorID, _ := middleware.ActorFromContext(ctx)
		taskInternal := int64(task.ID)
		if err := dbretry.InTx(ctx, deps.DB, "tasks.AddAgentActor", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).AddAgentActor(ctx, generated.AddAgentActorParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				AgentID:     sql.NullInt32{Int32: int32(agentID), Valid: true}, //#nosec G115 -- agent id is agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				Role:        role,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.TaskActorAdded,
				AuditAction:  "task.agent_actor.add",
				ResourceType: "task_actor",
				ResourceID:   pub.String(),
				TaskID:       &taskInternal,
				Payload: map[string]any{
					"taskId":  task.PublicID.String(),
					"actorId": pub.String(),
					"agentId": agentPub.String(),
					"kind":    "agent",
					"role":    in.Body.Role,
				},
				CallSite: "tasks.AddAgentActor",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &AddTaskAgentActorOutput{Body: TaskAgentActor{
			ID:      pub.String(),
			AgentID: agentPub.String(),
			Role:    in.Body.Role,
		}}, nil
	}
}

// ListAgentActors handles GET /tasks/{id}/agents.
func ListAgentActors(deps Deps) func(context.Context, *ListTaskAgentActorsInput) (*ListTaskAgentActorsOutput, error) {
	return func(ctx context.Context, in *ListTaskAgentActorsInput) (*ListTaskAgentActorsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		rows, err := deps.Queries.ListAgentActorsForTask(ctx, generated.ListAgentActorsForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskAgentActorsOutput{}
		out.Body.Agents = []TaskAgentActor{}
		for _, r := range rows {
			entry := TaskAgentActor{
				ID:        r.PublicID.String(),
				AgentID:   r.AgentPublicID.String(),
				AgentName: r.AgentName,
				Role:      string(r.Role),
				CreatedAt: r.CreatedAt.Unix(),
			}
			entry.UpdatedAt = nullTimeUnix(r.UpdatedAt)
			out.Body.Agents = append(out.Body.Agents, entry)
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
