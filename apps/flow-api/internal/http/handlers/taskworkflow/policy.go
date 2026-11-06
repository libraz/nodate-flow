package taskworkflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// SetCompletionPolicy handles POST /tasks/{id}/completion-policy.
func SetCompletionPolicy(deps Deps) func(context.Context, *CompletionPolicyInput) (*CompletionPolicyOutput, error) {
	return func(ctx context.Context, in *CompletionPolicyInput) (*CompletionPolicyOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		if in.Body.Policy != "manual" && in.Body.Policy != "review_required" {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		requiredIDs := make([]string, 0, len(in.Body.RequiredConstraintIDs))
		seen := make(map[string]struct{}, len(in.Body.RequiredConstraintIDs))
		for _, raw := range in.Body.RequiredConstraintIDs {
			parsed, parseErr := publicID(raw)
			if parseErr != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			canonical := parsed.String()
			if _, exists := seen[canonical]; exists {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			seen[canonical] = struct{}{}
			requiredIDs = append(requiredIDs, canonical)
		}
		idsJSON, err := json.Marshal(requiredIDs)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		var (
			out         CompletionPolicyOutput
			businessErr error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.SetCompletionPolicy", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			businessErr = nil
			qtx := generated.New(tx.RawTx())
			settings, queryErr := qtx.GetTaskWorkflowSettingsForUpdate(ctx, generated.GetTaskWorkflowSettingsForUpdateParams{
				WorkspaceID: ws.ID,
				ID:          task.ID,
			})
			if errors.Is(queryErr, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskNotFound)
				return errResponded
			}
			if queryErr != nil {
				return queryErr
			}
			// Weakening the review gate is held one role above editing.
			weakens, gateErr := weakensReviewGate(settings, in.Body.Policy, seen)
			if gateErr != nil {
				return gateErr
			}
			if weakens && !callerHasProjectRole(ctx, ws.Role, middleware.ProjectRoleLead) {
				businessErr = httpErr(apierrors.WsProjectAccessDenied)
				return errResponded
			}
			for _, rawID := range requiredIDs {
				constraintID, parseErr := publicID(rawID)
				if parseErr != nil {
					businessErr = httpErr(apierrors.ValidationBodyFieldInvalid)
					return errResponded
				}
				_, queryErr = qtx.FindEnabledTaskConstraintForWorkflow(ctx, generated.FindEnabledTaskConstraintForWorkflowParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
					PublicID:    constraintID,
				})
				if errors.Is(queryErr, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.ValidationBodyFieldInvalid)
					return errResponded
				}
				if queryErr != nil {
					return queryErr
				}
			}

			if _, queryErr := qtx.UpdateTaskCompletionPolicy(ctx, generated.UpdateTaskCompletionPolicyParams{
				CompletionPolicy:                generated.TasksCompletionPolicy(in.Body.Policy),
				RequiredCompletionConstraintIds: idsJSON,
				UpdatedByUserID:                 sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor id is a users.id key within the application's supported range
				WorkspaceID:                     ws.ID,
				ID:                              task.ID,
			}); queryErr != nil {
				return queryErr
			}

			if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
				task.PublicID.String(), eventbus.TaskUpdated, "task.completion_policy.update", "task", "taskworkflow.SetCompletionPolicy",
				map[string]any{
					"taskId":                task.PublicID.String(),
					"completionPolicy":      in.Body.Policy,
					"requiredConstraintIds": requiredIDs,
				}); err != nil {
				return err
			}
			out.Body = CompletionPolicy{
				Policy:                in.Body.Policy,
				WorkRevision:          settings.WorkRevision,
				RequiredConstraintIDs: requiredIDs,
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

// weakensReviewGate reports whether moving from settings to policy with the
// required set lowers a review_required gate or drops one of its constraints.
func weakensReviewGate(settings generated.GetTaskWorkflowSettingsForUpdateRow, policy string, required map[string]struct{}) (bool, error) {
	if settings.CompletionPolicy != generated.TasksCompletionPolicyReviewRequired {
		return false, nil
	}
	if policy != string(generated.TasksCompletionPolicyReviewRequired) {
		return true, nil
	}
	current, err := decodeStringArray(settings.RequiredCompletionConstraintIds)
	if err != nil {
		return false, err
	}
	for _, raw := range current {
		id, err := publicID(raw)
		if err != nil {
			return false, err
		}
		if _, kept := required[id.String()]; !kept {
			return true, nil
		}
	}
	return false, nil
}
