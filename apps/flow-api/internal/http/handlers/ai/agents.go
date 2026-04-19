package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// AgentSummary is the public DTO for an ai_agents row. Secrets and
// internal ids stay hidden; system_prompt round-trips so the edit UI
// can show the existing text without a separate detail endpoint.
type AgentSummary struct {
	ID                string   `json:"id" doc:"Agent public id (UUID v7)"`
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	SystemPrompt      string   `json:"systemPrompt"`
	ModelID           string   `json:"modelId"`
	ModelName         string   `json:"modelName"`
	ScheduleKind      string   `json:"scheduleKind"`
	Paused            bool     `json:"paused"`
	EventTriggerTypes []string `json:"eventTriggerTypes,omitempty"`
	CreatedAt         int64    `json:"createdAt"`
	UpdatedAt         *int64   `json:"updatedAt,omitempty"`
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
		// Bulk-fetch event_trigger_types in a single round-trip so the
		// list endpoint stays O(1) even if rows.length is large. The
		// generated ListAgents query doesn't expose the JSON column
		// today; this side query keeps the field optional without
		// regenerating sqlc.
		triggerByPub := map[string][]string{}
		if len(rows) > 0 {
			const tq = `SELECT public_id, event_trigger_types FROM ai_agents
				WHERE workspace_id = ? AND enabled = TRUE
				  AND event_trigger_types IS NOT NULL`
			tr, terr := deps.DB.QueryContext(ctx, tq, ws.ID)
			if terr == nil {
				defer tr.Close()
				for tr.Next() {
					var pub types.PublicID
					var raw json.RawMessage
					if err := tr.Scan(&pub, &raw); err == nil {
						var arr []string
						if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
							triggerByPub[pub.String()] = arr
						}
					}
				}
			}
		}
		out := &ListAgentsOutput{}
		out.Body.Agents = make([]AgentSummary, 0, len(rows))
		var total int64
		for _, r := range rows {
			if t, ok := r.Total.(int64); ok {
				total = t
			}
			out.Body.Agents = append(out.Body.Agents, AgentSummary{
				ID:                r.PublicID.String(),
				Name:              r.Name,
				Description:       nullStr(r.Description),
				SystemPrompt:      r.SystemPrompt,
				ModelID:           r.ModelPublicID.String(),
				ModelName:         r.ModelName,
				ScheduleKind:      string(r.ScheduleKind),
				Paused:            r.Paused,
				EventTriggerTypes: triggerByPub[r.PublicID.String()],
				CreatedAt:         r.CreatedAt.Unix(),
				UpdatedAt:         nullTimeUnixPtr(r.UpdatedAt),
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
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_agent.update_schedule",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_agent",
				ResourceID:   agentPub.String(),
				Metadata:     map[string]any{"scheduleKind": in.Body.ScheduleKind},
			})
		}
		out := &UpdateAgentScheduleOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// CreateAgentInput is the body for POST /workspaces/{wsId}/ai/agents.
type CreateAgentInput struct {
	WsID string `path:"wsId"`
	Body struct {
		ModelID           string   `json:"modelId" doc:"ai_models public id"`
		Name              string   `json:"name" minLength:"1" maxLength:"255"`
		Description       string   `json:"description,omitempty" maxLength:"1000"`
		SystemPrompt      string   `json:"systemPrompt" minLength:"1" maxLength:"16000"`
		Temperature       uint16   `json:"temperature,omitempty" minimum:"0" maximum:"200" doc:"Sampling temperature x100 (default 100)"`
		ScheduleKind      string   `json:"scheduleKind,omitempty" enum:"disabled,interval,on_event,manual" default:"disabled"`
		EventTriggerTypes []string `json:"eventTriggerTypes,omitempty" doc:"Eventbus kinds that fire this agent when scheduleKind=on_event"`
	}
}

// CreateAgentOutput returns the newly created agent summary.
type CreateAgentOutput struct {
	Body AgentSummary
}

// CreateAgent provisions a new ai_agents row bound to the given
// ai_models public id. The handler does not accept tools_json yet;
// agents mint tool access implicitly via their paired MCP token.
func CreateAgent(deps Deps) func(context.Context, *CreateAgentInput) (*CreateAgentOutput, error) {
	return func(ctx context.Context, in *CreateAgentInput) (*CreateAgentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		modelPub, err := types.Parse(in.Body.ModelID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		// Resolve the internal model id; the generated CreateAgent
		// query takes the internal FK.
		var (
			modelID   uint32
			modelName string
		)
		const q = `SELECT id, name FROM ai_models
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		if err := deps.DB.QueryRowContext(ctx, q, ws.ID, modelPub).Scan(&modelID, &modelName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AiModelNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		temp := in.Body.Temperature
		if temp == 0 {
			temp = 100
		}
		scheduleKind := in.Body.ScheduleKind
		if scheduleKind == "" {
			scheduleKind = "disabled"
		}
		pub := types.New()
		descNull := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		insertID, err := deps.Queries.CreateAgent(ctx, generated.CreateAgentParams{
			PublicID:        pub,
			WorkspaceID:     ws.ID,
			ModelID:         modelID,
			Name:            in.Body.Name,
			Description:     descNull,
			SystemPrompt:    in.Body.SystemPrompt,
			Temperature:     temp,
			MaxOutputTokens: sql.NullInt32{},
			ToolsJson:       json.RawMessage(`null`),
			ScheduleKind:    generated.AiAgentsScheduleKind(scheduleKind),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// Persist event_trigger_types via raw UPDATE so we don't have to
		// regenerate the sqlc CreateAgent shape until the column gets a
		// dedicated path. The id returned above scopes the update to
		// exactly the freshly inserted row.
		if len(in.Body.EventTriggerTypes) > 0 {
			raw, jerr := json.Marshal(in.Body.EventTriggerTypes)
			if jerr == nil {
				_, _ = deps.DB.ExecContext(ctx,
					`UPDATE ai_agents SET event_trigger_types = ? WHERE id = ?`,
					raw, insertID)
			}
		}
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_agent.create",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_agent",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"name": in.Body.Name},
			})
		}
		now := time.Now()
		out := &CreateAgentOutput{Body: AgentSummary{
			ID:                pub.String(),
			Name:              in.Body.Name,
			Description:       in.Body.Description,
			SystemPrompt:      in.Body.SystemPrompt,
			ModelID:           modelPub.String(),
			ModelName:         modelName,
			ScheduleKind:      scheduleKind,
			Paused:            false,
			EventTriggerTypes: in.Body.EventTriggerTypes,
			CreatedAt:         now.Unix(),
		}}
		return out, nil
	}
}

// UpdateAgentEventTriggersInput is the body for
// PATCH /workspaces/{wsId}/ai/agents/{agentId}/event-triggers.
type UpdateAgentEventTriggersInput struct {
	WsID    string `path:"wsId"`
	AgentID string `path:"agentId"`
	Body    struct {
		EventTriggerTypes []string `json:"eventTriggerTypes" doc:"Pass [] to clear; otherwise list of eventbus kinds"`
	}
}

// UpdateAgentEventTriggersOutput is the ack envelope.
type UpdateAgentEventTriggersOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// UpdateAgentEventTriggers replaces an agent's event_trigger_types
// JSON column. Passing an empty list clears the column (NULL) so the
// agent stops matching any on_event dispatch.
func UpdateAgentEventTriggers(deps Deps) func(context.Context, *UpdateAgentEventTriggersInput) (*UpdateAgentEventTriggersOutput, error) {
	return func(ctx context.Context, in *UpdateAgentEventTriggersInput) (*UpdateAgentEventTriggersOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		agentPub, err := types.Parse(in.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		var raw any
		if len(in.Body.EventTriggerTypes) == 0 {
			raw = nil
		} else {
			b, jerr := json.Marshal(in.Body.EventTriggerTypes)
			if jerr != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			raw = b
		}
		const q = `UPDATE ai_agents
			SET event_trigger_types = ?
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE`
		res, err := deps.DB.ExecContext(ctx, q, raw, ws.ID, agentPub)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_agent.update_triggers",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_agent",
				ResourceID:   agentPub.String(),
			})
		}
		out := &UpdateAgentEventTriggersOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// TriggerAgentInput is the path for
// POST /workspaces/{wsId}/ai/agents/{agentId}/trigger.
type TriggerAgentInput struct {
	WsID    string `path:"wsId"`
	AgentID string `path:"agentId"`
}

// TriggerAgentOutput is the ack envelope.
type TriggerAgentOutput struct {
	Body struct {
		Ok bool `json:"ok"`
		// DedupeKey is echoed back so operators can correlate the
		// manual trigger with the row that lands in agent_runs (or
		// the synchronous log line).
		DedupeKey string `json:"dedupeKey"`
	}
}

// TriggerAgent enqueues (or synchronously dispatches) one run for
// the given agent. Honors the paused kill switch and refuses when
// neither a Queue nor a synchronous Runner is wired.
func TriggerAgent(deps Deps, queue agentruntime.Queue, runner agentruntime.Runner) func(context.Context, *TriggerAgentInput) (*TriggerAgentOutput, error) {
	return func(ctx context.Context, in *TriggerAgentInput) (*TriggerAgentOutput, error) {
		if queue == nil && runner == nil {
			return nil, httpErr(apierrors.AiAgentRuntimeDisabled)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		agentPub, err := types.Parse(in.AgentID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		// Resolve internal id + paused in a single round-trip so the
		// scheduler's DBSource and this handler share the same
		// source of truth.
		var (
			agentID uint32
			paused  bool
		)
		const sel = `SELECT id, paused FROM ai_agents
			WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		if err := deps.DB.QueryRowContext(ctx, sel, ws.ID, agentPub).Scan(&agentID, &paused); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AiAgentNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if paused {
			return nil, httpErr(apierrors.AiAgentPaused)
		}
		now := time.Now().UTC()
		dedupeKey := fmt.Sprintf("%d:manual:%d", agentID, now.UnixNano())
		job := agentruntime.Job{AgentID: agentID, WsID: ws.ID}
		if queue != nil {
			if err := queue.Enqueue(ctx, agentruntime.Run{
				DedupeKey:   dedupeKey,
				Job:         job,
				ScheduledAt: now,
			}); err != nil && !errors.Is(err, agentruntime.ErrDuplicate) {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		} else {
			if err := runner.Run(ctx, job, now); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		out := &TriggerAgentOutput{}
		out.Body.Ok = true
		out.Body.DedupeKey = dedupeKey
		return out, nil
	}
}

// Models list endpoint so the agents create dialog can populate a
// model picker without hitting every provider individually.

// ModelSummary is the public DTO for an ai_models row.
type ModelSummary struct {
	ID           string `json:"id" doc:"Model public id (UUID v7)"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	ProviderID   string `json:"providerId"`
	ProviderKind string `json:"providerKind"`
}

// ListModelsInput is the query for GET /workspaces/{wsId}/ai/models.
type ListModelsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListModelsOutput is the response for GET /workspaces/{wsId}/ai/models.
type ListModelsOutput struct {
	Body struct {
		Total  int64          `json:"total"`
		Models []ModelSummary `json:"models"`
	}
}

// ListModels lists every enabled ai_models row across all providers
// in the workspace. The agents create dialog uses this to populate a
// Select; workspaces with zero models get an empty list and the UI
// points operators at the provider registration flow.
func ListModels(deps Deps) func(context.Context, *ListModelsInput) (*ListModelsOutput, error) {
	return func(ctx context.Context, in *ListModelsInput) (*ListModelsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		const q = `
			SELECT m.public_id, m.name, m.display_name,
			       p.public_id AS provider_public_id, p.kind AS provider_kind,
			       COUNT(*) OVER() AS total
			FROM ai_models m
			INNER JOIN ai_providers p ON p.id = m.provider_id AND p.enabled = TRUE
			WHERE m.workspace_id = ? AND m.enabled = TRUE
			ORDER BY m.sort_weight ASC, m.created_at DESC, m.public_id DESC
			LIMIT ? OFFSET ?`
		rows, err := deps.DB.QueryContext(ctx, q, ws.ID, in.Limit, in.Offset)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer rows.Close()
		out := &ListModelsOutput{}
		out.Body.Models = make([]ModelSummary, 0)
		var total int64
		for rows.Next() {
			var (
				modelPub    types.PublicID
				name        string
				displayName string
				providerPub types.PublicID
				providerKnd string
				row         int64
			)
			if err := rows.Scan(&modelPub, &name, &displayName, &providerPub, &providerKnd, &row); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			total = row
			out.Body.Models = append(out.Body.Models, ModelSummary{
				ID:           modelPub.String(),
				Name:         name,
				DisplayName:  displayName,
				ProviderID:   providerPub.String(),
				ProviderKind: providerKnd,
			})
		}
		if err := rows.Err(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out.Body.Total = total
		return out, nil
	}
}
