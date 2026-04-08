package ai

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// AgentSummary is the public DTO for an ai_agents row. Secrets and
// internal ids stay hidden; system_prompt round-trips so the edit UI
// can show the existing text without a separate detail endpoint.
type AgentSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	SystemPrompt string `json:"systemPrompt"`
	ModelID      string `json:"modelId"`
	ModelName    string `json:"modelName"`
	ScheduleKind string `json:"scheduleKind"`
	Paused       bool   `json:"paused"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    *int64 `json:"updatedAt,omitempty"`
}

// ListAgentsInput is the query for GET /workspaces/{wsId}/ai/agents.
type ListAgentsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListAgentsOutput is the response for GET /workspaces/{wsId}/ai/agents.
type ListAgentsOutput struct {
	Body struct {
		Total  int64          `json:"total"`
		Agents []AgentSummary `json:"agents"`
	}
}

// ListAgents lists the workspace's AI agents.
func ListAgents(deps Deps) func(context.Context, *ListAgentsInput) (*ListAgentsOutput, error) {
	return func(ctx context.Context, in *ListAgentsInput) (*ListAgentsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListAgentsForWorkspace(ctx, generated.ListAgentsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListAgentsOutput{}
		out.Body.Agents = make([]AgentSummary, 0, len(rows))
		var total int64
		for _, r := range rows {
			if t, ok := r.Total.(int64); ok {
				total = t
			}
			out.Body.Agents = append(out.Body.Agents, AgentSummary{
				ID:           r.PublicID.String(),
				Name:         r.Name,
				Description:  nullStr(r.Description),
				SystemPrompt: r.SystemPrompt,
				ModelID:      r.ModelPublicID.String(),
				ModelName:    r.ModelName,
				ScheduleKind: string(r.ScheduleKind),
				Paused:       r.Paused,
				CreatedAt:    r.CreatedAt.Unix(),
				UpdatedAt:    nullTimeUnixPtr(r.UpdatedAt),
			})
		}
		out.Body.Total = total
		return out, nil
	}
}

func nullTimeUnixPtr(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	u := t.Time.Unix()
	return &u
}

// UpdateAgentScheduleInput is the body for
// PATCH /workspaces/{wsId}/ai/agents/{agentId}/schedule.
type UpdateAgentScheduleInput struct {
	WsID    string `path:"wsId"`
	AgentID string `path:"agentId"`
	Body    struct {
		ScheduleKind string `json:"scheduleKind" enum:"disabled,interval,on_event,manual"`
	}
}

// UpdateAgentScheduleOutput is the ack envelope.
type UpdateAgentScheduleOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// UpdateAgentSchedule flips an agent's trigger mode. Pair with the
// global NF_AGENT_TICK_INTERVAL to fire every enabled interval agent
// once per tick.
func UpdateAgentSchedule(deps Deps) func(context.Context, *UpdateAgentScheduleInput) (*UpdateAgentScheduleOutput, error) {
	return func(ctx context.Context, in *UpdateAgentScheduleInput) (*UpdateAgentScheduleOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		agentPub, err := types.Parse(in.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		const q = `UPDATE ai_agents
			SET schedule_kind = ?
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE`
		res, err := deps.DB.ExecContext(ctx, q, in.Body.ScheduleKind, ws.ID, agentPub)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		out := &UpdateAgentScheduleOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// unused placeholders to keep imports stable even if a handler branch
// is removed during refactors. Not referenced at runtime.
var (
	_ = errors.New
	_ = time.Now
)
