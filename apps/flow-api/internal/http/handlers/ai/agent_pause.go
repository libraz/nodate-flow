package ai

import (
	"context"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

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
		const q = `UPDATE ai_agents
			SET paused = ?
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE`
		res, err := deps.DB.ExecContext(ctx, q, in.Body.Paused, ws.ID, agentPub)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if n == 0 {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		kind := eventbus.AiAgentResumed
		if in.Body.Paused {
			kind = eventbus.AiAgentPaused
		}
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        kind,
			WorkspaceID: ws.ID,
			Payload: map[string]any{
				"agentId": agentPub.String(),
				"paused":  in.Body.Paused,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "ai.PauseAgent"),
				slog.String("event_type", string(kind)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("agent_id", agentPub.String()),
			)
		}
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_agent.pause",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_agent",
				ResourceID:   agentPub.String(),
				Metadata:     map[string]any{"paused": in.Body.Paused},
			})
		}
		out := &PauseAgentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
