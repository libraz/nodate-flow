// Agent handoff endpoints. Three operations:
//
//   - POST /tasks/{id}/handoff/to-agent — promote an AI agent to the task's
//     assignee, disabling any prior agent assignee.
//   - POST /tasks/{id}/handoff/to-user — agent (or human acting on its
//     behalf) hands the task back to a user, optionally upserting a new
//     human assignee row.
//   - GET  /tasks/{id}/agent-runs — recent agent run events scoped to the
//     task.
//
// All three honour the existing workspace / task ACL middleware: callers
// land on the standard task context (ws + task) populated by
// RequireTaskAccess.

package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// HandoffToAgent handles POST /tasks/{id}/handoff/to-agent.
//
// Atomicity: the disable-prior + upsert-new pair and the corresponding
// event row are committed in a single transaction so external observers
// never see a partial handoff. The event INSERT runs through the same
// qtx so the deadlock retry contract matches every other write path in
// this package.
func HandoffToAgent(deps Deps) func(context.Context, *HandoffToAgentInput) (*HandoffToAgentOutput, error) {
	return func(ctx context.Context, in *HandoffToAgentInput) (*HandoffToAgentOutput, error) {
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
			return nil, httpErr(apierrors.AiAgentNotFound)
		}
		agentInternalID, err := deps.Queries.FindAgentIDByPublicIDForWorkspace(ctx,
			generated.FindAgentIDByPublicIDForWorkspaceParams{
				WorkspaceID: ws.ID,
				PublicID:    agentPub,
			})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AiAgentNotFound, apierrors.InternalUnexpected))
		}

		// Survey existing agent assignees so we can detect the no-op
		// case (same agent already assigned) and capture the prior
		// assignee for the event payload.
		existing, err := deps.Queries.ListAgentsAssignedToTask(ctx, generated.ListAgentsAssignedToTaskParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		var (
			priorAgentPublicID string
			priorAgentExists   bool
		)
		for _, a := range existing {
			if a.ID == agentInternalID {
				return nil, httpErr(apierrors.WsTaskAgentAlreadyAssigned)
			}
			if !priorAgentExists {
				priorAgentPublicID = a.PublicID.String()
				priorAgentExists = true
			}
		}

		newActorPub := types.New()
		eventPub := types.New()
		payload := map[string]any{
			"taskId":         task.PublicID.String(),
			"agentPublicId":  agentPub.String(),
			"actorPublicId":  newActorPub.String(),
			"priorAgentKind": nil,
		}
		if priorAgentExists {
			payload["priorAgentPublicId"] = priorAgentPublicID
			payload["priorAgentKind"] = "agent"
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)

		// Disable any prior agent assignee rows on the task. Soft-disable
		// is sufficient — task_actors.public_id is preserved so audit
		// history remains queryable.
		for _, a := range existing {
			if _, err := tx.ExecContext(ctx,
				`UPDATE task_actors SET enabled = FALSE
				 WHERE workspace_id = ? AND task_id = ? AND agent_id = ? AND kind = 'agent' AND enabled = TRUE`,
				ws.ID, task.ID, a.ID,
			); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		if _, err := qtx.AddAgentActor(ctx, generated.AddAgentActorParams{
			PublicID:    newActorPub,
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			AgentID:     sql.NullInt32{Int32: int32(agentInternalID), Valid: true}, //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
			Role:        generated.TaskActorsRoleAssignee,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID := sql.NullInt32{Int32: int32(task.ID), Valid: true} //#nosec G115 -- tasks.id is BIGINT UNSIGNED, fits int32 within realistic deployments
		actorUserID := sql.NullInt32{}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			actorUserID = sql.NullInt32{Int32: int32(aID), Valid: true} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		}
		if _, err := qtx.InsertHandoffToAgentEvent(ctx, generated.InsertHandoffToAgentEventParams{
			PublicID:    eventPub,
			WorkspaceID: ws.ID,
			TaskID:      taskID,
			ActorUserID: actorUserID,
			PayloadJson: payloadJSON,
			OccurredAt:  time.Now().UTC(),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.handoff.to_agent",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				Metadata:     map[string]any{"agentId": agentPub.String()},
			})
		}

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &HandoffToAgentOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// HandoffToUser handles POST /tasks/{id}/handoff/to-user.
//
// The handler disables the current agent assignee (returns
// WS_TASK_AGENT_NOT_ASSIGNED when none exists), optionally upserts a
// user assignee, updates tasks.agent_memo with the handoff status, and
// emits an agent.task.handoff_to_user event tagged with the prior agent
// as actor. Wrapped in a single transaction so observers see either the
// full handoff or none of it.
func HandoffToUser(deps Deps) func(context.Context, *HandoffToUserInput) (*HandoffToUserOutput, error) {
	return func(ctx context.Context, in *HandoffToUserInput) (*HandoffToUserOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		existing, err := deps.Queries.ListAgentsAssignedToTask(ctx, generated.ListAgentsAssignedToTaskParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if len(existing) == 0 {
			return nil, httpErr(apierrors.WsTaskAgentNotAssigned)
		}
		priorAgent := existing[0]

		// Resolve optional target user up front so a bad public id
		// surfaces as 404 WS.MEMBER.NOT_FOUND before we mutate anything.
		var (
			targetUserID  uint32
			targetUserPub types.PublicID
			hasTargetUser bool
		)
		if in.Body.TargetUserPublicID != "" {
			pub, perr := types.Parse(in.Body.TargetUserPublicID)
			if perr != nil {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			uid, uerr := deps.Queries.FindUserInternalIdByPublicId(ctx, pub)
			if uerr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(uerr, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
			}
			targetUserID = uid
			targetUserPub = pub
			hasTargetUser = true
		}

		now := time.Now().UTC()
		eventPub := types.New()
		payload := map[string]any{
			"taskId":             task.PublicID.String(),
			"reason":             in.Body.Reason,
			"priorAgentPublicId": priorAgent.PublicID.String(),
		}
		if hasTargetUser {
			payload["targetUserPublicId"] = targetUserPub.String()
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		memoPatch := map[string]any{
			"handoff_status": "handed_back",
			"handoff_reason": in.Body.Reason,
			"handed_back_at": now.Unix(),
		}
		memoJSON, err := json.Marshal(memoPatch)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)

		// Disable every current agent assignee row so the task no
		// longer routes new events to an agent.
		for _, a := range existing {
			if _, err := tx.ExecContext(ctx,
				`UPDATE task_actors SET enabled = FALSE
				 WHERE workspace_id = ? AND task_id = ? AND agent_id = ? AND kind = 'agent' AND enabled = TRUE`,
				ws.ID, task.ID, a.ID,
			); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		if hasTargetUser {
			actorPub := types.New()
			if _, err := qtx.AddActor(ctx, generated.AddActorParams{
				PublicID:    actorPub,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				UserID:      sql.NullInt32{Int32: int32(targetUserID), Valid: true}, //#nosec G115 -- users.id is BIGINT UNSIGNED, fits int32 within realistic deployments
				Role:        generated.TaskActorsRoleAssignee,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		if err := qtx.UpdateTaskAgentMemo(ctx, generated.UpdateTaskAgentMemoParams{
			Column1:     memoJSON,
			WorkspaceID: ws.ID,
			ID:          task.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID := sql.NullInt32{Int32: int32(task.ID), Valid: true} //#nosec G115 -- tasks.id is BIGINT UNSIGNED, fits int32 within realistic deployments
		if _, err := qtx.InsertHandoffToUserEvent(ctx, generated.InsertHandoffToUserEventParams{
			PublicID:     eventPub,
			WorkspaceID:  ws.ID,
			TaskID:       taskID,
			ActorAgentID: sql.NullInt32{Int32: int32(priorAgent.ID), Valid: true}, //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
			PayloadJson:  payloadJSON,
			OccurredAt:   now,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.handoff.to_user",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				Metadata: map[string]any{
					"reason":             in.Body.Reason,
					"priorAgentPublicId": priorAgent.PublicID.String(),
				},
			})
		}

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &HandoffToUserOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// ListAgentRuns handles GET /tasks/{id}/agent-runs. Returns recent
// agent run events scoped to this task. Until the orchestrator pipeline
// (Task #7 / #18) stamps task_id + actor_agent_id on its ai.agent.run.*
// events, this list will be empty even for tasks with active agents —
// the handler still returns a well-shaped envelope so callers can wire
// in advance.
func ListAgentRuns(deps Deps) func(context.Context, *ListAgentRunsInput) (*ListAgentRunsOutput, error) {
	return func(ctx context.Context, in *ListAgentRunsInput) (*ListAgentRunsOutput, error) {
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
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		taskID := sql.NullInt32{Int32: int32(task.ID), Valid: true} //#nosec G115 -- tasks.id is BIGINT UNSIGNED, fits int32 within realistic deployments
		rows, err := deps.Queries.ListAgentRunsByTask(ctx, generated.ListAgentRunsByTaskParams{
			WorkspaceID: ws.ID,
			TaskID:      taskID,
			Limit:       limit,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				out := &ListAgentRunsOutput{}
				out.Body.Runs = []AgentRunEvent{}
				return out, nil
			}
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "ListAgentRunsByTask failed",
				slog.Any("err", err),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListAgentRunsOutput{}
		out.Body.Runs = make([]AgentRunEvent, 0, len(rows))
		for _, r := range rows {
			out.Body.Runs = append(out.Body.Runs, AgentRunEvent{
				EventID:    r.PublicID.String(),
				Type:       r.Type,
				OccurredAt: r.OccurredAt.Unix(),
				Agent: AgentRef{
					ID:   r.AgentPublicID.String(),
					Name: r.AgentName.String,
				},
				PayloadJSON: string(r.PayloadJson),
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
