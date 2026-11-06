package taskworkflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	handoffexpiry "github.com/libraz/nodate-flow/apps/flow-api/internal/taskworkflow"
)

// CreateHandoff handles POST /tasks/{id}/handoffs.
func CreateHandoff(deps Deps) func(context.Context, *CreateHandoffInput) (*CreateHandoffOutput, error) {
	return func(ctx context.Context, in *CreateHandoffInput) (*CreateHandoffOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		if !validHandoffRole(in.Body.Role) {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		toPublicID, err := publicID(in.Body.ToUserID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		var requestedFromPublicID types.PublicID
		if in.Body.FromUserID != "" {
			requestedFromPublicID, err = publicID(in.Body.FromUserID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
		}
		if in.Body.DueAt != nil && *in.Body.DueAt <= 0 {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		toUserID, err := deps.Queries.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    toPublicID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if toUserID > math.MaxInt32 {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		var (
			out         CreateHandoffOutput
			businessErr error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.CreateHandoff", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			businessErr = nil
			out = CreateHandoffOutput{}
			qtx := generated.New(tx.RawTx())
			if err := lockTask(ctx, qtx, ws.ID, task.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.WsTaskNotFound)
					return errResponded
				}
				return err
			}

			role := generated.TaskActorsRole(in.Body.Role)
			currentActors, err := qtx.ListTaskHumanActorsForRole(ctx, generated.ListTaskHumanActorsForRoleParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				Role:        role,
			})
			if err != nil {
				return err
			}
			if len(currentActors) == 0 {
				businessErr = httpErr(apierrors.WsTaskActorNotFound)
				return errResponded
			}

			var source generated.ListTaskHumanActorsForRoleRow
			sourceFound := false
			if requestedFromPublicID != (types.PublicID{}) {
				for _, candidate := range currentActors {
					if candidate.UserPublicID == requestedFromPublicID.String() {
						source = candidate
						sourceFound = true
						break
					}
				}
			} else {
				for _, candidate := range currentActors {
					if candidate.UserID.Valid && int64(candidate.UserID.Int32) == int64(actorID) {
						source = candidate
						sourceFound = true
						break
					}
				}
				if !sourceFound && isElevatedWorkspaceRole(ws.Role) && len(currentActors) == 1 {
					source = currentActors[0]
					sourceFound = true
				}
				if !sourceFound && isElevatedWorkspaceRole(ws.Role) && len(currentActors) > 1 {
					businessErr = httpErr(apierrors.WsTaskUpdateConflict)
					return errResponded
				}
			}
			if !sourceFound || !source.UserID.Valid || source.UserID.Int32 <= 0 {
				businessErr = httpErr(apierrors.WsTaskActorNotFound)
				return errResponded
			}
			if int64(source.UserID.Int32) != int64(actorID) && !isElevatedWorkspaceRole(ws.Role) {
				businessErr = httpErr(apierrors.WsTaskAccessDenied)
				return errResponded
			}
			if int64(source.UserID.Int32) == int64(toUserID) {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			}

			if _, err := qtx.FindPendingTaskHandoffForRole(ctx, generated.FindPendingTaskHandoffForRoleParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				Role:        generated.TaskHandoffsRole(in.Body.Role),
			}); err == nil {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			if err := authorizeTaskReader(ctx, tx.RawTx(), task.PublicID, toUserID); err != nil {
				businessErr = err
				return errResponded
			}
			fromPublicID, err := qtx.GetTaskWorkflowUserPublicID(ctx, uint32(source.UserID.Int32)) //#nosec G115 -- source user id is positive int32 after the guard above
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskActorNotFound)
				return errResponded
			}
			if err != nil {
				return err
			}
			requesterPublicID, err := qtx.GetTaskWorkflowUserPublicID(ctx, actorID)
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsWorkspaceAccessDenied)
				return errResponded
			}
			if err != nil {
				return err
			}
			var dueAt sql.NullTime
			if in.Body.DueAt != nil {
				dueAt = sql.NullTime{Time: time.Unix(*in.Body.DueAt, 0).UTC(), Valid: true}
			}
			handoffPublicID := types.New()
			if err := qtx.CreateTaskHandoff(ctx, generated.CreateTaskHandoffParams{
				PublicID:          handoffPublicID,
				WorkspaceID:       ws.ID,
				TaskID:            task.ID,
				Role:              generated.TaskHandoffsRole(in.Body.Role),
				FromUserID:        source.UserID,
				ToUserID:          sql.NullInt32{Int32: int32(toUserID), Valid: true}, //#nosec G115 -- checked against MaxInt32 above
				RequestedByUserID: nullableUserID(actorID),
				DueAt:             dueAt,
			}); err != nil {
				return err
			}
			var dueAtValue *int64
			if in.Body.DueAt != nil {
				value := *in.Body.DueAt
				dueAtValue = &value
			}
			if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
				handoffPublicID.String(), eventbus.TaskHandoffRequested, "task.handoff.request", "task_handoff", "taskworkflow.CreateHandoff",
				map[string]any{
					"taskId":                  task.PublicID.String(),
					"handoffId":               handoffPublicID.String(),
					"role":                    in.Body.Role,
					"fromUserPublicId":        fromPublicID,
					"toUserPublicId":          toPublicID.String(),
					"requestedByUserPublicId": requesterPublicID,
					"targetUserPublicId":      toPublicID.String(),
					"dueAt":                   dueAtValue,
				}); err != nil {
				return err
			}
			out.Body = HandoffBody{
				ID:         handoffPublicID.String(),
				TaskID:     task.PublicID.String(),
				Role:       in.Body.Role,
				FromUserID: fromPublicID,
				ToUserID:   toPublicID.String(),
				DueAt:      dueAtValue,
				Status:     "pending",
				CreatedAt:  time.Now().UTC().Unix(),
			}
			return nil
		})
		if errors.Is(txErr, errResponded) {
			return nil, businessErr
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &out, nil
	}
}

// DecideHandoff handles POST /tasks/{id}/handoffs/{handoffId}/decision.
func DecideHandoff(deps Deps) func(context.Context, *HandoffDecisionInput) (*HandoffDecisionOutput, error) {
	return func(ctx context.Context, in *HandoffDecisionInput) (*HandoffDecisionOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		handoffPublicID, err := publicID(in.HandoffID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		status := generated.TaskHandoffsStatus("")
		decision := in.Body.Decision
		switch decision {
		case "accept":
			status = generated.TaskHandoffsStatusAccepted
		case "decline":
			status = generated.TaskHandoffsStatusDeclined
		case "cancel":
			status = generated.TaskHandoffsStatusCancelled
		default:
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		var (
			out         HandoffDecisionOutput
			businessErr error
			expired     bool
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.DecideHandoff", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			businessErr = nil
			expired = false
			out = HandoffDecisionOutput{}
			qtx := generated.New(tx.RawTx())
			if err := lockTask(ctx, qtx, ws.ID, task.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.WsTaskNotFound)
					return errResponded
				}
				return err
			}
			handoff, err := qtx.FindTaskHandoffForUpdate(ctx, generated.FindTaskHandoffForUpdateParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    handoffPublicID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskNotFound)
				return errResponded
			}
			if err != nil {
				return err
			}
			if handoff.Status != generated.TaskHandoffsStatusPending {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			}
			if decision == "cancel" {
				if !callerCanCancelHandoff(ctx, ws.Role, actorID, handoff.FromUserID, handoff.RequestedByUserID) {
					businessErr = httpErr(apierrors.WsTaskAccessDenied)
					return errResponded
				}
			} else if !handoff.ToUserID.Valid || int64(handoff.ToUserID.Int32) != int64(actorID) {
				businessErr = httpErr(apierrors.WsTaskAccessDenied)
				return errResponded
			}
			if err := authorizeTaskReader(ctx, tx.RawTx(), task.PublicID, actorID); err != nil {
				businessErr = err
				return errResponded
			}
			if decision == "accept" {
				now := time.Now().UTC().Truncate(time.Millisecond)
				if handoff.DueAt.Valid && !handoff.DueAt.Time.After(now) {
					// The expiry commits; the refusal is answered after it.
					expiredNow, err := handoffexpiry.ExpireHandoffTx(ctx, tx, deps.Events, handoffexpiry.DueHandoff{
						ID:               handoff.ID,
						WorkspaceID:      handoff.WorkspaceID,
						TaskID:           handoff.TaskID,
						HandoffPublicID:  handoff.HandoffPublicID,
						TaskPublicID:     task.PublicID.String(),
						Role:             handoff.Role,
						FromUserPublicID: dbString(handoff.FromUserPublicID),
						ToUserPublicID:   dbString(handoff.ToUserPublicID),
					}, now, "taskworkflow.DecideHandoff")
					if err != nil {
						return err
					}
					if !expiredNow {
						return fmt.Errorf("taskworkflow: overdue handoff %s was not expired under its lock", handoff.HandoffPublicID)
					}
					expired = true
					return nil
				}
			}

			if decision == "accept" {
				if !handoff.FromUserID.Valid || int64(handoff.FromUserID.Int32) == int64(actorID) {
					businessErr = httpErr(apierrors.WsTaskUpdateConflict)
					return errResponded
				}
				sourceActor, err := qtx.FindTaskHumanActorForRole(ctx, generated.FindTaskHumanActorForRoleParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					UserID:      handoff.FromUserID,
					Role:        generated.TaskActorsRole(handoff.Role),
				})
				if errors.Is(err, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.WsTaskUpdateConflict)
					return errResponded
				}
				if err != nil {
					return err
				}
				removed, err := qtx.RemoveTaskHumanActorForHandoff(ctx, generated.RemoveTaskHumanActorForHandoffParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					UserID:      handoff.FromUserID,
					Role:        generated.TaskActorsRole(handoff.Role),
				})
				if err != nil {
					return err
				}
				if removed != 1 {
					businessErr = httpErr(apierrors.WsTaskUpdateConflict)
					return errResponded
				}
				isTargetActor, err := qtx.IsEnabledTaskHumanActorForHandoff(ctx, generated.IsEnabledTaskHumanActorForHandoffParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					UserID:      handoff.ToUserID,
					Role:        generated.TaskActorsRole(handoff.Role),
				})
				if err != nil {
					return err
				}
				if !isTargetActor {
					newActorPublicID := types.New()
					if _, err := qtx.AddActor(ctx, generated.AddActorParams{
						PublicID:    newActorPublicID,
						WorkspaceID: ws.ID,
						TaskID:      task.ID,
						UserID:      handoff.ToUserID,
						Role:        generated.TaskActorsRole(handoff.Role),
					}); err != nil {
						return err
					}
					if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
						newActorPublicID.String(), eventbus.TaskActorAdded, "task.handoff.actor.add", "task_actor", "taskworkflow.DecideHandoff",
						map[string]any{
							"taskId":  task.PublicID.String(),
							"actorId": newActorPublicID.String(),
							"userId":  dbString(handoff.ToUserPublicID),
							"role":    string(handoff.Role),
						}); err != nil {
						return err
					}
				}
				if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
					sourceActor.ActorPublicID, eventbus.TaskActorRemoved, "task.handoff.actor.remove", "task_actor", "taskworkflow.DecideHandoff",
					map[string]any{
						"taskId":  task.PublicID.String(),
						"actorId": sourceActor.ActorPublicID,
						"userId":  dbString(handoff.FromUserPublicID),
						"role":    string(handoff.Role),
					}); err != nil {
					return err
				}
			}

			resolved, err := qtx.ResolveTaskHandoff(ctx, generated.ResolveTaskHandoffParams{
				Status:      status,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    handoffPublicID,
			})
			if err != nil {
				return err
			}
			if resolved != 1 {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			}
			// A withdrawal is news to the recipient; a decision is news to the holder.
			target := dbString(handoff.FromUserPublicID)
			if decision == "cancel" {
				target = dbString(handoff.ToUserPublicID)
			}
			if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
				handoffPublicID.String(), eventbus.TaskHandoffDecided, "task.handoff.decide", "task_handoff", "taskworkflow.DecideHandoff",
				map[string]any{
					"taskId":             task.PublicID.String(),
					"handoffId":          handoffPublicID.String(),
					"role":               string(handoff.Role),
					"decision":           decision,
					"fromUserPublicId":   dbString(handoff.FromUserPublicID),
					"toUserPublicId":     dbString(handoff.ToUserPublicID),
					"targetUserPublicId": target,
				}); err != nil {
				return err
			}
			if decision == "accept" {
				// Actor transfer, domain events, and dependent constraint state
				// commit together. A failed refresh rolls the whole acceptance
				// back instead of leaving derived constraint state stale.
				if err := refreshTaskConstraints(ctx, qtx, ws.ID, task.ID); err != nil {
					return err
				}
			}
			var dueAtValue *int64
			if handoff.DueAt.Valid {
				value := handoff.DueAt.Time.UTC().Unix()
				dueAtValue = &value
			}
			out.Body = HandoffBody{
				ID:         handoff.HandoffPublicID,
				TaskID:     task.PublicID.String(),
				Role:       string(handoff.Role),
				FromUserID: dbString(handoff.FromUserPublicID),
				ToUserID:   dbString(handoff.ToUserPublicID),
				DueAt:      dueAtValue,
				Status:     string(status),
				CreatedAt:  handoff.CreatedAt.UTC().Unix(),
				ResolvedAt: nowInt64Ptr(),
			}
			return nil
		})
		if errors.Is(txErr, errResponded) {
			return nil, businessErr
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if expired {
			return nil, httpErr(apierrors.WsTaskTransitionRejected)
		}
		return &out, nil
	}
}

// CancelPendingHandoffFromHolderTx cancels the pending handoff of role whose
// source is holderUserID, appending its decision event in tx. The caller holds
// the task lock and has removed that holder's role in the same transaction.
func CancelPendingHandoffFromHolderTx(
	ctx context.Context,
	tx *dbretry.Tx,
	events *mutationlog.Recorder,
	actorID, workspaceID, taskID uint32,
	taskPublicID string,
	role generated.TaskActorsRole,
	holderUserID sql.NullInt32,
) error {
	if !holderUserID.Valid || !validHandoffRole(string(role)) {
		return nil
	}
	qtx := generated.New(tx.RawTx())
	pending, err := qtx.FindPendingTaskHandoffFromHolderForUpdate(ctx, generated.FindPendingTaskHandoffFromHolderForUpdateParams{
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		Role:        generated.TaskHandoffsRole(role),
		FromUserID:  holderUserID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	resolved, err := qtx.ResolveTaskHandoff(ctx, generated.ResolveTaskHandoffParams{
		Status:      generated.TaskHandoffsStatusCancelled,
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		PublicID:    pending.PublicID,
	})
	if err != nil {
		return err
	}
	if resolved != 1 {
		return fmt.Errorf("taskworkflow: cancel affected %d rows for handoff %s", resolved, pending.HandoffPublicID)
	}
	return recordWorkflowEvent(ctx, tx, Deps{Events: events}, actorID, workspaceID, taskID,
		pending.HandoffPublicID, eventbus.TaskHandoffDecided, "task.handoff.cancel", "task_handoff", "taskworkflow.CancelPendingHandoffFromHolderTx",
		map[string]any{
			"taskId":             taskPublicID,
			"handoffId":          pending.HandoffPublicID,
			"role":               string(role),
			"decision":           "cancel",
			"fromUserPublicId":   dbString(pending.FromUserPublicID),
			"toUserPublicId":     dbString(pending.ToUserPublicID),
			"targetUserPublicId": dbString(pending.ToUserPublicID),
		})
}

// callerCanCancelHandoff reports whether actorID may withdraw a pending
// handoff: its source holder, its requester, a project lead or a workspace
// admin.
func callerCanCancelHandoff(ctx context.Context, workspaceRole middleware.WorkspaceRole, actorID uint32, fromUserID, requestedByUserID sql.NullInt32) bool {
	for _, id := range []sql.NullInt32{fromUserID, requestedByUserID} {
		if id.Valid && int64(id.Int32) == int64(actorID) {
			return true
		}
	}
	return callerHasProjectRole(ctx, workspaceRole, middleware.ProjectRoleLead)
}

func validHandoffRole(role string) bool {
	return role == "assignee" || role == "reviewer" || role == "approver"
}

func nowInt64Ptr() *int64 {
	now := time.Now().UTC().Unix()
	return &now
}
