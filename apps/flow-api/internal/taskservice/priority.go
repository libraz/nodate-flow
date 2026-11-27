package taskservice

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
)

// SetPriority writes one task field through taskupdate.Apply after
// reauthorizing the current task, membership, project role, and (for an
// agent) assignment inside the same retryable transaction.
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
		find := generated.FindTaskByPublicIdParams{WorkspaceID: principal.WorkspaceID(), PublicID: target}
		current, err := qtx.FindTaskByPublicId(ctx, find)
		if err != nil {
			return err
		}
		if current.Priority != priority {
			actor := mutationlog.Actor{UserID: principal.UserID(), WorkspaceID: principal.WorkspaceID()}
			extra := map[string]any{"priority": priority}
			if authz.Agent != nil {
				actor = mutationlog.Actor{AgentID: authz.Agent.ID, WorkspaceID: principal.WorkspaceID()}
				extra["actorKind"] = "agent"
				extra["actorAgentId"] = authz.Agent.PublicID.String()
				if authz.DelegatedByPublicID != (types.PublicID{}) {
					extra["delegatedByUserId"] = authz.DelegatedByPublicID.String()
				}
			}
			res, err := taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID:  principal.WorkspaceID(),
				TaskID:       authz.Task.ID,
				PublicID:     target,
				Patch:        taskupdate.Patch{Priority: &priority},
				Actor:        actor,
				Via:          "taskservice",
				ExtraPayload: extra,
			})
			if err != nil {
				return err
			}
			tx.AfterCommit(func() {
				s.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
					AuditAction:  "task.update",
					ResourceType: "task",
					ResourceID:   target.String(),
					Payload:      res.Payload,
					CallSite:     "taskservice.SetPriority (taskupdate.Apply appended the event)",
				})
			})
			if current, err = qtx.FindTaskByPublicId(ctx, find); err != nil {
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
