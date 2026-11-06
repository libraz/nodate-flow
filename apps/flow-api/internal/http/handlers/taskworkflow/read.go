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

// GetWorkflow handles GET /tasks/{id}/workflow.
func GetWorkflow(deps Deps) func(context.Context, *GetWorkflowInput) (*GetWorkflowOutput, error) {
	return func(ctx context.Context, _ *GetWorkflowInput) (*GetWorkflowOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		canEdit := callerCanEditTask(ctx, ws.Role)
		var (
			out         WorkflowSnapshot
			businessErr error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.GetWorkflow",
			&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
			func(ctx context.Context, tx *dbretry.Tx) error {
				businessErr = nil
				qtx := generated.New(tx)
				settings, queryErr := qtx.GetTaskWorkflowSettings(ctx, generated.GetTaskWorkflowSettingsParams{
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
				requiredIDs, err := decodeStringArray(settings.RequiredCompletionConstraintIds)
				if err != nil {
					return err
				}

				latestTransition, transitionErr := qtx.GetLatestTaskTransitionForSubmission(ctx, generated.GetLatestTaskTransitionForSubmissionParams{
					WorkspaceID: ws.ID,
					TaskID:      sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- tasks.id is an INT UNSIGNED key within the application's supported range
				})
				if transitionErr != nil && !errors.Is(transitionErr, sql.ErrNoRows) {
					return transitionErr
				}
				if errors.Is(transitionErr, sql.ErrNoRows) {
					latestTransition = ""
				}

				out = WorkflowSnapshot{
					CompletionPolicy:      string(settings.CompletionPolicy),
					WorkRevision:          settings.WorkRevision,
					RequiredConstraintIDs: requiredIDs,
					RecentSubmissions:     make([]WorkflowSubmission, 0),
					Handoffs:              make([]WorkflowHandoff, 0),
					Capabilities: WorkflowCapabilities{
						CanRequestHandoffRoles: make([]string, 0),
					},
				}
				out.Capabilities.CanSetPolicy = canEdit
				out.Capabilities.CanSubmit = canEdit && canSubmitFromState(settings.CompletionPolicy, settings.DerivedState, latestTransition)

				submissions, queryErr := qtx.ListRecentTaskSubmissionsForWorkflow(ctx, generated.ListRecentTaskSubmissionsForWorkflowParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
				})
				if queryErr != nil {
					return queryErr
				}
				for _, row := range submissions {
					submission, decodeErr := decodeWorkflowSubmission(row)
					if decodeErr != nil {
						return decodeErr
					}
					out.RecentSubmissions = append(out.RecentSubmissions, submission)
					if out.CurrentSubmission == nil && submission.Revision == settings.WorkRevision {
						current := submission
						out.CurrentSubmission = &current
					}
				}
				if out.CurrentSubmission != nil {
					reviewerPublicID := out.CurrentSubmission.ReviewerUserID
					if reviewerPublicID != "" {
						actorPublicID, queryErr := qtx.GetTaskWorkflowUserPublicID(ctx, actorID)
						if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
							return queryErr
						}
						decisionPending := out.CurrentSubmission.Decision == nil
						if queryErr == nil && actorPublicID == reviewerPublicID && decisionPending {
							isReviewer, queryErr := qtx.IsCurrentTaskWorkflowReviewer(ctx, generated.IsCurrentTaskWorkflowReviewerParams{
								WorkspaceID: ws.ID,
								TaskID:      task.ID,
								UserID:      nullableUserID(actorID),
							})
							if queryErr != nil {
								return queryErr
							}
							out.Capabilities.CanDecideCurrentSubmission = isReviewer
						}
					}
				}

				handoffs, queryErr := qtx.ListRecentTaskWorkflowHandoffs(ctx, generated.ListRecentTaskWorkflowHandoffsParams{
					WorkspaceID: ws.ID,
					TaskID:      task.ID,
				})
				if queryErr != nil {
					return queryErr
				}
				for _, row := range handoffs {
					handoff := WorkflowHandoff{
						ID:         row.HandoffPublicID,
						Role:       string(row.Role),
						FromUserID: dbString(row.FromUserPublicID),
						ToUserID:   dbString(row.ToUserPublicID),
						Status:     string(row.Status),
						CreatedAt:  row.CreatedAt.UTC().Unix(),
						CanDecide:  row.Status == generated.TaskHandoffsStatusPending && row.ToUserID.Valid && int64(row.ToUserID.Int32) == int64(actorID),
						CanCancel:  row.Status == generated.TaskHandoffsStatusPending && callerCanCancelHandoff(ctx, ws.Role, actorID, row.FromUserID, row.RequestedByUserID),
					}
					if row.DueAt.Valid {
						value := row.DueAt.Time.UTC().Unix()
						handoff.DueAt = &value
					}
					if row.ResolvedAt.Valid {
						value := row.ResolvedAt.Time.UTC().Unix()
						handoff.ResolvedAt = &value
					}
					out.Handoffs = append(out.Handoffs, handoff)
				}

				if canEdit {
					for _, role := range []string{"assignee", "reviewer", "approver"} {
						actors, queryErr := qtx.ListTaskHumanActorsForRole(ctx, generated.ListTaskHumanActorsForRoleParams{
							WorkspaceID: ws.ID,
							TaskID:      task.ID,
							Role:        generated.TaskActorsRole(role),
						})
						if queryErr != nil {
							return queryErr
						}
						for _, actor := range actors {
							if isElevatedWorkspaceRole(ws.Role) || (actor.UserID.Valid && int64(actor.UserID.Int32) == int64(actorID)) {
								out.Capabilities.CanRequestHandoffRoles = append(out.Capabilities.CanRequestHandoffRoles, role)
								break
							}
						}
					}
				}
				return nil
			},
		)
		if errors.Is(txErr, errResponded) {
			return nil, businessErr
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &GetWorkflowOutput{Body: out}, nil
	}
}

func canSubmitFromState(policy generated.TasksCompletionPolicy, state generated.TasksDerivedState, latestTransition string) bool {
	if policy != generated.TasksCompletionPolicyReviewRequired {
		return false
	}
	switch state {
	case generated.TasksDerivedStateOpen, generated.TasksDerivedStateReview:
		return true
	case generated.TasksDerivedStateWaiting:
		return latestTransition != string(eventbus.TaskTransitionBlock)
	default:
		return false
	}
}

func decodeWorkflowSubmission(row generated.ListRecentTaskSubmissionsForWorkflowRow) (WorkflowSubmission, error) {
	criteria, err := decodeStringArray(row.AcceptanceCriteriaJson)
	if err != nil {
		return WorkflowSubmission{}, err
	}
	evidence, err := decodeStringArray(row.EvidenceUrlsJson)
	if err != nil {
		return WorkflowSubmission{}, err
	}
	attachments := make([]SubmissionAttachment, 0)
	if err := json.Unmarshal(row.AttachmentDigestsJson, &attachments); err != nil {
		return WorkflowSubmission{}, err
	}
	decisionValue := dbString(row.Decision)
	submission := WorkflowSubmission{
		ID:                 row.SubmissionPublicID,
		Revision:           row.WorkRevision,
		CreatedAt:          row.CreatedAt.UTC().Unix(),
		SubmitterUserID:    dbString(row.SubmitterUserPublicID),
		ReviewerUserID:     dbString(row.ReviewerUserPublicID),
		AcceptanceCriteria: criteria,
		EvidenceURLs:       evidence,
		Attachments:        attachments,
	}
	if decisionValue != "" {
		decision := &WorkflowDecision{
			Decision:        decisionValue,
			Reason:          row.Reason,
			DecidedByUserID: dbString(row.DecidedByUserPublicID),
		}
		if row.DecidedAt.Valid {
			value := row.DecidedAt.Time.UTC().Unix()
			decision.DecidedAt = &value
		}
		submission.Decision = decision
	}
	return submission, nil
}

func callerCanEditTask(ctx context.Context, workspaceRole middleware.WorkspaceRole) bool {
	return callerHasProjectRole(ctx, workspaceRole, middleware.ProjectRoleEditor)
}

// callerHasProjectRole reports whether the caller meets minRole on the task's
// project; workspace owners and admins always do.
func callerHasProjectRole(ctx context.Context, workspaceRole middleware.WorkspaceRole, minRole middleware.ProjectRole) bool {
	if isElevatedWorkspaceRole(workspaceRole) {
		return true
	}
	project, ok := middleware.ProjectFromContext(ctx)
	return ok && project.Role.AtLeast(minRole)
}

func decodeStringArray(raw json.RawMessage) ([]string, error) {
	out := make([]string, 0)
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = make([]string, 0)
	}
	return out, nil
}
