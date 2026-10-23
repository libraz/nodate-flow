package ai

import (
	"context"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// errAgentNotFound rolls a pause back when the agent is not there, so no
// event or audit row describes it.
var errAgentNotFound = errors.New("ai: agent not found")

// PauseAgentInput is the body for POST /workspaces/{wsId}/ai/agents/{agentId}/pause.
type PauseAgentInput struct {
	WsID    string `path:"wsId"`
	AgentID string `path:"agentId"`
	Body    struct {
		Paused bool `json:"paused"`
	}
}

// PauseAgentOutput is the ack envelope.
type PauseAgentOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// PauseAgent flips the `paused` column on an ai_agents row. The
// agentguard package consults this on every MCP tool invocation, so
// flipping the bit is the kill switch contract for runaway agents.
//
// The handler uses raw SQL rather than a sqlc query so this slice
// can land without a sqlc regen pass.
func PauseAgent(deps Deps) func(context.Context, *PauseAgentInput) (*PauseAgentOutput, error) {
	return func(ctx context.Context, in *PauseAgentInput) (*PauseAgentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		agentPub, err := types.Parse(in.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		kind := eventbus.AiAgentResumed
		if in.Body.Paused {
			kind = eventbus.AiAgentPaused
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		const q = `UPDATE ai_agents
			SET paused = ?
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE`
		err = dbretry.InTx(ctx, deps.DB, "ai.PauseAgent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			res, err := tx.ExecContext(ctx, q, in.Body.Paused, ws.ID, agentPub)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				exists, err := agentExists(ctx, tx, ws.ID, agentPub)
				if err != nil {
					return err
				}
				if !exists {
					return errAgentNotFound
				}
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    kind,
				AuditAction:  "ai_agent.pause",
				ResourceType: "ai_agent",
				ResourceID:   agentPub.String(),
				Payload: map[string]any{
					"agentId": agentPub.String(),
					"paused":  in.Body.Paused,
				},
				CallSite: "ai.PauseAgent",
			})
		})
		if errors.Is(err, errAgentNotFound) {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &PauseAgentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
