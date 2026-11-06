package taskworkflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// CreateSubmission handles POST /tasks/{id}/submissions.
func CreateSubmission(deps Deps) func(context.Context, *CreateSubmissionInput) (*CreateSubmissionOutput, error) {
	return func(ctx context.Context, in *CreateSubmissionInput) (*CreateSubmissionOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		reviewerPublicID, err := publicID(in.Body.ReviewerUserID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		criteria, err := validateAcceptanceCriteria(in.Body.AcceptanceCriteria)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		evidenceURLs, err := validateEvidenceURLs(in.Body.EvidenceURLs)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		attachmentIDs := make([]types.PublicID, 0, len(in.Body.AttachmentIDs))
		seenAttachments := make(map[string]struct{}, len(in.Body.AttachmentIDs))
		for _, rawID := range in.Body.AttachmentIDs {
			attachmentID, parseErr := publicID(rawID)
			if parseErr != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			canonical := attachmentID.String()
			if _, exists := seenAttachments[canonical]; exists {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			seenAttachments[canonical] = struct{}{}
			attachmentIDs = append(attachmentIDs, attachmentID)
		}

		criteriaJSON, err := json.Marshal(criteria)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		evidenceJSON, err := json.Marshal(evidenceURLs)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		var (
			out         CreateSubmissionOutput
			businessErr error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.CreateSubmission", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			businessErr = nil
			out = CreateSubmissionOutput{}
			qtx := generated.New(tx.RawTx())
			if err := lockTask(ctx, qtx, ws.ID, task.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.WsTaskNotFound)
					return errResponded
				}
				return err
			}
			settings, err := qtx.GetTaskWorkflowSettingsForUpdate(ctx, generated.GetTaskWorkflowSettingsForUpdateParams{
				WorkspaceID: ws.ID,
				ID:          task.ID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskNotFound)
				return errResponded
			}
			if err != nil {
				return err
			}
			if string(settings.CompletionPolicy) != "review_required" {
				businessErr = httpErr(apierrors.WsTaskTransitionRejected)
				return errResponded
			}

			reviewer, err := qtx.FindTaskWorkflowReviewer(ctx, generated.FindTaskWorkflowReviewerParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    reviewerPublicID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskActorNotFound)
				return errResponded
			}
			if err != nil {
				return err
			}
			if reviewer.ID == actorID {
				businessErr = httpErr(apierrors.ValidationBodyFieldInvalid)
				return errResponded
			}
			if err := authorizeTaskReader(ctx, tx.RawTx(), task.PublicID, reviewer.ID); err != nil {
				businessErr = err
				return errResponded
			}

			attachments := make([]SubmissionAttachment, 0, len(attachmentIDs))
			for _, attachmentID := range attachmentIDs {
				row, queryErr := qtx.FindTaskAttachmentDigestForSubmission(ctx, generated.FindTaskAttachmentDigestForSubmissionParams{
					WorkspaceID: ws.ID,
					TaskID:      sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- task id is an INT UNSIGNED key within the application's supported range
					PublicID:    attachmentID,
				})
				if errors.Is(queryErr, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.ValidationBodyFieldInvalid)
					return errResponded
				}
				if queryErr != nil {
					return queryErr
				}
				attachments = append(attachments, SubmissionAttachment{ID: row.AttachmentPublicID, SHA256: row.Sha256})
			}
			attachmentsJSON, err := json.Marshal(attachments)
			if err != nil {
				return err
			}

			submitterPublicID, err := qtx.GetTaskWorkflowUserPublicID(ctx, actorID)
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsWorkspaceAccessDenied)
				return errResponded
			}
			if err != nil {
				return err
			}
			spec, err := taskstate.PrepareSubmissionTx(ctx, tx, taskstate.SubmissionParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    types.FromUUID(task.PublicID),
				ActorUserID: handlerutil.ActorPtr(ctx),
				Via:         "api",
			})
			if err != nil {
				return err
			}
			if spec != nil {
				businessErr = httpErr(spec)
				return errResponded
			}

			submissionPublicID := types.New()
			if _, err := qtx.CreateTaskSubmission(ctx, generated.CreateTaskSubmissionParams{
				PublicID:               submissionPublicID,
				WorkspaceID:            ws.ID,
				TaskID:                 task.ID,
				WorkRevision:           settings.WorkRevision,
				SubmittedByUserID:      nullableUserID(actorID),
				ReviewerUserID:         nullableUserID(reviewer.ID),
				AcceptanceCriteriaJson: criteriaJSON,
				EvidenceUrlsJson:       evidenceJSON,
				AttachmentDigestsJson:  attachmentsJSON,
			}); err != nil {
				return err
			}
			if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
				submissionPublicID.String(), eventbus.TaskSubmissionCreated, "task.submission.create", "task_submission", "taskworkflow.CreateSubmission",
				map[string]any{
					"taskId":             task.PublicID.String(),
					"submissionId":       submissionPublicID.String(),
					"revision":           settings.WorkRevision,
					"submitterUserId":    submitterPublicID,
					"reviewerUserId":     reviewer.UserPublicID,
					"targetUserPublicId": reviewer.UserPublicID,
				}); err != nil {
				return err
			}
			out.Body = SubmissionBody{
				ID:                 submissionPublicID.String(),
				TaskID:             task.PublicID.String(),
				Revision:           settings.WorkRevision,
				SubmitterUserID:    submitterPublicID,
				ReviewerUserID:     reviewer.UserPublicID,
				AcceptanceCriteria: append([]string(nil), criteria...),
				EvidenceURLs:       append([]string(nil), evidenceURLs...),
				Attachments:        attachments,
				CreatedAt:          time.Now().UTC().Unix(),
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

// DecideSubmission handles POST /tasks/{id}/submissions/{submissionId}/decision.
func DecideSubmission(deps Deps) func(context.Context, *ReviewDecisionInput) (*ReviewDecisionOutput, error) {
	return func(ctx context.Context, in *ReviewDecisionInput) (*ReviewDecisionOutput, error) {
		ws, task, actorID, err := workflowContext(ctx)
		if err != nil {
			return nil, err
		}
		submissionPublicID, err := publicID(in.SubmissionID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		decision := ""
		switch in.Body.Decision {
		case "approve":
			decision = "approved"
		case "reject":
			decision = "rejected"
		default:
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		reason := strings.TrimSpace(in.Body.Reason)
		if utf8.RuneCountInString(reason) > 2000 {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}

		var (
			out         ReviewDecisionOutput
			businessErr error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "taskworkflow.DecideSubmission", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			businessErr = nil
			out = ReviewDecisionOutput{}
			qtx := generated.New(tx.RawTx())
			if err := lockTask(ctx, qtx, ws.ID, task.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					businessErr = httpErr(apierrors.WsTaskNotFound)
					return errResponded
				}
				return err
			}
			submission, err := qtx.FindTaskSubmissionForDecision(ctx, generated.FindTaskSubmissionForDecisionParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    submissionPublicID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				businessErr = httpErr(apierrors.WsTaskNotFound)
				return errResponded
			}
			if err != nil {
				return err
			}
			if !submission.ReviewerUserID.Valid || int64(submission.ReviewerUserID.Int32) != int64(actorID) {
				businessErr = httpErr(apierrors.WsTaskAccessDenied)
				return errResponded
			}
			if submission.SubmissionRevision != submission.CurrentWorkRevision {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			}
			if actorID > math.MaxInt32 {
				businessErr = httpErr(apierrors.WsTaskAccessDenied)
				return errResponded
			}
			isReviewer, err := qtx.IsCurrentTaskWorkflowReviewer(ctx, generated.IsCurrentTaskWorkflowReviewerParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				UserID:      sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actorID is rejected above if greater than MaxInt32
			})
			if err != nil {
				return err
			}
			if !isReviewer {
				businessErr = httpErr(apierrors.WsTaskAccessDenied)
				return errResponded
			}
			if _, err := qtx.FindTaskReviewDecisionForSubmission(ctx, generated.FindTaskReviewDecisionForSubmissionParams{
				WorkspaceID:  ws.ID,
				TaskID:       task.ID,
				SubmissionID: submission.SubmissionID,
			}); err == nil {
				businessErr = httpErr(apierrors.WsTaskUpdateConflict)
				return errResponded
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			decisionPublicID := types.New()
			if err := qtx.CreateTaskReviewDecision(ctx, generated.CreateTaskReviewDecisionParams{
				PublicID:        decisionPublicID,
				WorkspaceID:     ws.ID,
				TaskID:          task.ID,
				SubmissionID:    submission.SubmissionID,
				DecidedByUserID: nullableUserID(actorID),
				Decision:        generated.TaskReviewDecisionsDecision(decision),
				Reason:          sql.NullString{String: reason, Valid: reason != ""},
			}); err != nil {
				return err
			}
			reviewerPublicID, err := qtx.GetTaskWorkflowUserPublicID(ctx, actorID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := recordWorkflowEvent(ctx, tx, deps, actorID, ws.ID, task.ID,
				decisionPublicID.String(), eventbus.TaskReviewDecided, "task.review.decide", "task_review_decision", "taskworkflow.DecideSubmission",
				map[string]any{
					"taskId":             task.PublicID.String(),
					"submissionId":       submissionPublicID.String(),
					"decisionId":         decisionPublicID.String(),
					"decision":           decision,
					"reviewerUserId":     reviewerPublicID,
					"submitterUserId":    dbString(submission.SubmitterUserPublicID),
					"targetUserPublicId": dbString(submission.SubmitterUserPublicID),
				}); err != nil {
				return err
			}
			out.Body = DecisionBody{
				ID:           decisionPublicID.String(),
				SubmissionID: submissionPublicID.String(),
				Decision:     decision,
				Reason:       reason,
				DecidedAt:    time.Now().UTC().Unix(),
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

func validateAcceptanceCriteria(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > 30 {
		return nil, errors.New("acceptance criteria must contain between 1 and 30 entries")
	}
	out := make([]string, 0, len(input))
	for _, criterion := range input {
		criterion = strings.TrimSpace(criterion)
		if criterion == "" || utf8.RuneCountInString(criterion) > 1000 {
			return nil, errors.New("acceptance criterion must contain between 1 and 1000 characters")
		}
		out = append(out, criterion)
	}
	return out, nil
}

func validateEvidenceURLs(input []string) ([]string, error) {
	if len(input) > 20 {
		return nil, errors.New("evidence links are limited to 20 entries")
	}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > 2048 {
			return nil, errors.New("evidence link must contain between 1 and 2048 bytes")
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("evidence link must be an absolute HTTP(S) URL without user information")
		}
		out = append(out, value)
	}
	return out, nil
}
