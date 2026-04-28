// AI agent assignment on tasks via task_actors (kind='agent').
// DELETE reuses RemoveActor since task_actors rows are addressed by
// public_id regardless of kind.

package tasks

import (
	"context"
	"log/slog"

	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
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
		if _, err := deps.Queries.AddAgentActor(ctx, generated.AddAgentActorParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			AgentID:     sql.NullInt32{Int32: int32(agentID), Valid: true}, //#nosec G115 -- agent id is agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Role:        role,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskActorAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":  task.PublicID.String(),
				"actorId": pub.String(),
				"agentId": agentPub.String(),
				"kind":    "agent",
				"role":    in.Body.Role,
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.AddAgentActor"),
				slog.String("event_type", string(eventbus.TaskActorAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("actor_public_id", pub.String()),
				slog.String("agent_public_id", agentPub.String()),
			)
		}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.agent_actor.add",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_actor",
				ResourceID:   pub.String(),
			})
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
