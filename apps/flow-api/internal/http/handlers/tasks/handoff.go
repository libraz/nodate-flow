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

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// HandoffToAgent handles POST /tasks/{id}/handoff/to-agent.
//
// Atomicity: the disable-prior + upsert-new pair and the corresponding
// event row are committed in a single transaction so external observers
// never see a partial handoff. The event goes through the shared recorder,
// so its notification, webhook and stream fan-out follow the commit.
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
		payload := map[string]any{
			"taskId":         task.PublicID.String(),
			"agentId":        agentPub.String(),
			"agentPublicId":  agentPub.String(),
			"actorPublicId":  newActorPub.String(),
			"priorAgentKind": nil,
		}
		if priorAgentExists {
			payload["priorAgentPublicId"] = priorAgentPublicID
			payload["priorAgentKind"] = "agent"
		}

		actorID, _ := middleware.ActorFromContext(ctx)
		taskInternal := int64(task.ID)
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.HandoffToAgent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			// Disable any prior agent assignee rows on the task. Soft-disable
			// is sufficient — task_actors.public_id is preserved so audit
			// history remains queryable.
			//
			// The rows were listed before the transaction opened, and the
			// UPDATE runs the same predicate the listing did, so a zero count
			// is that row having been disabled in between. The count is
			// summed rather than checked per row: displacing some of the
			// priors is still a handoff, and it is only when this request
			// displaced none of the ones it read that its picture of the
			// task's assignment is stale — and the event below already names
			// one of them as the agent this handoff took over from.
			displaced := int64(0)
			for _, a := range existing {
				res, err := tx.ExecContext(ctx,
					`UPDATE task_actors SET enabled = FALSE
					 WHERE workspace_id = ? AND task_id = ? AND agent_id = ? AND kind = 'agent' AND enabled = TRUE`,
					ws.ID, task.ID, a.ID,
				)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				displaced += n
			}
			if len(existing) > 0 && displaced == 0 {
				answered = httpErr(apierrors.WsTaskUpdateConflict)
				return errAnswered
			}

			if _, err := generated.New(tx).AddAgentActor(ctx, generated.AddAgentActorParams{
				PublicID:    newActorPub,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				AgentID:     sql.NullInt32{Int32: int32(agentInternalID), Valid: true}, //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
				Role:        generated.TaskActorsRoleAssignee,
			}); err != nil {
				return err
			}

			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.AgentTaskHandoffToAgent,
				AuditAction:  "task.handoff.to_agent",
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				TaskID:       &taskInternal,
				Payload:      payload,
				CallSite:     "tasks.HandoffToAgent",
			})
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
//
// A task that stops being live between the ACL that resolved it and the
// memo write answers WS_TASK_NOT_FOUND: see [recordHandoffToUser].
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
			// Resolve scoped to the task's workspace so a handoff cannot
			// reassign the task to a user from another tenant.
			uid, uerr := deps.Queries.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if uerr != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(uerr, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
			}
			targetUserID = uid
			targetUserPub = pub
			hasTargetUser = true
		}

		now := time.Now().UTC()
		payload := map[string]any{
			"taskId":             task.PublicID.String(),
			"reason":             in.Body.Reason,
			"priorAgentPublicId": priorAgent.PublicID.String(),
		}
		if hasTargetUser {
			payload["targetUserPublicId"] = targetUserPub.String()
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

		actorID, _ := middleware.ActorFromContext(ctx)
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.HandoffToUser", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			// Disable every current agent assignee row so the task no
			// longer routes new events to an agent.
			//
			// Taking the agent off the task is what this endpoint does, so a
			// run that disabled nothing handed nothing back: the rows were
			// listed before the transaction opened and the UPDATE runs the
			// same predicate, so zero across all of them means another
			// handoff got there first. Refused with the same code the empty
			// listing above answers with, because it is the same state seen a
			// moment later — and the rollback drops the handoff event and the
			// memo patch that would otherwise record a second hand-back.
			handedBack := int64(0)
			for _, a := range existing {
				res, err := tx.ExecContext(ctx,
					`UPDATE task_actors SET enabled = FALSE
					 WHERE workspace_id = ? AND task_id = ? AND agent_id = ? AND kind = 'agent' AND enabled = TRUE`,
					ws.ID, task.ID, a.ID,
				)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				handedBack += n
			}
			if handedBack == 0 {
				answered = httpErr(apierrors.WsTaskAgentNotAssigned)
				return errAnswered
			}

			if hasTargetUser {
				if _, err := generated.New(tx).AddActor(ctx, generated.AddActorParams{
					PublicID:    types.New(),
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					UserID:      sql.NullInt32{Int32: int32(targetUserID), Valid: true}, //#nosec G115 -- users.id is BIGINT UNSIGNED, fits int32 within realistic deployments
					Role:        generated.TaskActorsRoleAssignee,
				}); err != nil {
					return err
				}
			}

			if err := recordHandoffToUser(ctx, tx, deps.Mutations, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, handoffToUserWrites{
				taskID:       task.ID,
				taskPublicID: task.PublicID.String(),
				priorAgentID: priorAgent.ID,
				memoPatch:    memoJSON,
				payload:      payload,
			}); err != nil {
				var pd *handlerutil.ProblemDetails
				if errors.As(err, &pd) {
					answered = err
					return errAnswered
				}
				return err
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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

// handoffToUserWrites are the two rows that record a hand-back: the
// state the task carries afterwards, and the event that announces it.
type handoffToUserWrites struct {
	taskID       uint32
	taskPublicID string
	priorAgentID uint32
	memoPatch    json.RawMessage
	payload      map[string]any
}

// recordHandoffToUser writes the hand-back state and then the event that
// announces it, and answers with the error the request fails on.
//
// The memo statement matches an enabled task only, so it can succeed and
// change nothing. The task was resolved through the task ACL before this
// request opened its transaction, so a zero count is that task having been
// disabled or removed since — the same condition the handler answers with
// WS.TASK.NOT_FOUND when it cannot resolve a task at all, seen a moment
// later.
//
// The order is what makes that answer worth giving. The event is the
// visible half of a hand-back and the memo patch is the state behind it,
// so writing the event first would leave a request that under-recorded
// looking, to every reader, exactly like one that succeeded: an
// announcement on the timeline with a handoff_status that never changed.
// Refusing here instead rolls the whole transaction back and records
// neither.
//
// The event is the prior agent's act, so it is attributed to the agent;
// the user who drove the request is named by the audit row.
func recordHandoffToUser(ctx context.Context, tx *dbretry.Tx, rec *mutationlog.Recorder, actor mutationlog.Actor, w handoffToUserWrites) error {
	memoRows, err := generated.New(tx).UpdateTaskAgentMemo(ctx, generated.UpdateTaskAgentMemoParams{
		Column1:     w.memoPatch,
		WorkspaceID: actor.WorkspaceID,
		ID:          w.taskID,
	})
	if err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	if memoRows == 0 {
		return httpErr(apierrors.WsTaskNotFound)
	}

	taskInternal := int64(w.taskID)
	priorAgent := int64(w.priorAgentID)
	return rec.RecordInTx(ctx, tx, actor, mutationlog.Mutation{
		EventType:    eventbus.AgentTaskHandoffToUser,
		AuditAction:  "task.handoff.to_user",
		ResourceType: "task",
		ResourceID:   w.taskPublicID,
		TaskID:       &taskInternal,
		ActorAgentID: &priorAgent,
		Payload:      w.payload,
		CallSite:     "tasks.HandoffToUser",
	})
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
			// LEFT JOIN signals produces an all-zero PublicID when the
			// event has no triggering signal (most agent runs predate ADR
			// 0008 D4). Treat the zero value as SQL NULL so the JSON shape
			// is `null` rather than the misleading
			// "00000000-0000-0000-0000-000000000000" sentinel.
			var triggeredBySignal *string
			if r.TriggeredBySignalPublicID != (types.PublicID{}) {
				v := r.TriggeredBySignalPublicID.String()
				triggeredBySignal = &v
			}
			var sysSource *string
			if r.ActorSystemSource.Valid {
				v := r.ActorSystemSource.String
				sysSource = &v
			}
			// Same null-vs-zero rationale as triggeredBySignal above: the
			// LEFT self-join against events_e_rev produces an all-zero
			// PublicID for non-reversal rows, which we surface as JSON
			// `null` rather than the misleading nil-UUID sentinel.
			var reversesEvent *string
			if r.ReversesEventPublicID != (types.PublicID{}) {
				v := r.ReversesEventPublicID.String()
				reversesEvent = &v
			}
			out.Body.Runs = append(out.Body.Runs, AgentRunEvent{
				EventID:    r.PublicID.String(),
				Type:       r.Type,
				OccurredAt: r.OccurredAt.Unix(),
				Agent: AgentRef{
					ID:   r.AgentPublicID.String(),
					Name: r.AgentName.String,
				},
				ActorSystemSource:   sysSource,
				TriggeredBySignalID: triggeredBySignal,
				ReversesEventID:     reversesEvent,
				WasReversed:         r.WasReversed,
				PayloadJSON:         string(r.PayloadJson),
			})
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}
