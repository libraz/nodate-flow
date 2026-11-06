package taskservice

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// SetPriority writes one task field after reauthorizing the current task,
// membership, project role, and (for an agent) assignment inside the same
// retryable transaction.
func (s *Service) SetPriority(ctx context.Context, principal execution.Principal, target types.PublicID, priority int32) (Task, error) {
	if priority < MinPriority || priority > MaxPriority {
		return Task{}, ErrPriorityOutOfRange
	}
	if err := s.checkReadiness(true); err != nil {
		return Task{}, err
	}
	var out Task
	err := dbretry.InTx(ctx, s.DB, "taskservice.SetPriority", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		authz, err := execution.AuthorizeTaskTx(ctx, tx, principal, target, execution.CapabilitySetPriority)
		if err != nil {
			return err
		}
		qtx := s.Queries.WithTx(tx.RawTx())
		current, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: principal.WorkspaceID(),
			PublicID:    target,
		})
		if err != nil {
			return err
		}
		if current.Priority != priority {
			updatedByUser := sql.NullInt32{}
			if userID := principal.UserID(); userID != 0 {
				updatedByUser = sql.NullInt32{Int32: int32(userID), Valid: true} //#nosec G115 -- users.id is INT UNSIGNED and fits int32 within realistic deployments
			}
			if _, err := qtx.UpdateTask(ctx, generated.UpdateTaskParams{
				Title:           current.Title,
				Description:     current.Description,
				Priority:        priority,
				DueOn:           current.DueOn,
				StartedOn:       current.StartedOn,
				SortWeight:      current.SortWeight,
				Visibility:      current.Visibility,
				UpdatedByUserID: updatedByUser,
				WorkspaceID:     principal.WorkspaceID(),
				PublicID:        target,
			}); err != nil {
				return err
			}
			taskID := internalTaskID(authz.Task.ID)
			mutation := mutationlog.Mutation{
				EventType:    eventbus.TaskUpdated,
				AuditAction:  "task.update",
				ResourceType: "task",
				ResourceID:   target.String(),
				TaskID:       &taskID,
				Payload: map[string]any{
					"taskId":   target.String(),
					"priority": priority,
					"via":      "taskservice",
				},
				CallSite: "taskservice.SetPriority",
			}
			actor := mutationlog.Actor{UserID: principal.UserID(), WorkspaceID: principal.WorkspaceID()}
			if authz.Agent != nil {
				agentID := internalAgentID(authz.Agent.ID)
				mutation.ActorAgentID = &agentID
				mutation.Payload["actorKind"] = "agent"
				mutation.Payload["actorAgentId"] = authz.Agent.PublicID.String()
				if authz.DelegatedByPublicID != (types.PublicID{}) {
					mutation.Payload["delegatedByUserId"] = authz.DelegatedByPublicID.String()
				}
			}
			if err := s.Mutations.RecordInTx(ctx, tx, actor, mutation); err != nil {
				return err
			}
			current, err = qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: principal.WorkspaceID(),
				PublicID:    target,
			})
			if err != nil {
				return err
			}
		}
		out, err = taskFromRow(current)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}
