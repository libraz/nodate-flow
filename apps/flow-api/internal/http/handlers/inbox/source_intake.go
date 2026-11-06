package inbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/sourceintake"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// CaptureAsIntakeRequestBody is the optional body for source capture.
type CaptureAsIntakeRequestBody struct {
	ExistingTaskID string `json:"existingTaskId,omitempty" doc:"Optional task public id to link this source to. Requires current task visibility and project-editor permission."`
}

// CaptureAsIntakeInput is the request for POST
// /workspaces/{wsId}/inbox/{signalId}/intake.
type CaptureAsIntakeInput struct {
	WsID     string `path:"wsId"`
	SignalID string `path:"signalId"`
	Body     *CaptureAsIntakeRequestBody
}

type CaptureAsIntakeBody struct {
	IntakeItemID string `json:"intakeItemId"`
	Title        string `json:"title"`
	Body         string `json:"body,omitempty"`
	SourceURL    string `json:"sourceUrl,omitempty"`
	TaskID       string `json:"taskId,omitempty"`
	Created      bool   `json:"created"`
}

// CaptureAsIntakeOutput is the response for source capture. A new item
// returns 201; an existing item or link replay returns 200.
type CaptureAsIntakeOutput struct {
	Status int `header:"Status,omitempty"`
	Body   CaptureAsIntakeBody
}

// CaptureAsIntake creates (or returns) the intake row uniquely associated
// with an inbox signal. Linking to an existing task — the requested one, or
// the one the signal already names — is an editor mutation: task visibility
// and project role are re-checked under locks inside the linking transaction.
func CaptureAsIntake(deps Deps) func(context.Context, *CaptureAsIntakeInput) (*CaptureAsIntakeOutput, error) {
	return func(ctx context.Context, in *CaptureAsIntakeInput) (*CaptureAsIntakeOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		workspaceID, err := resolve.WorkspaceMemberForWrite(ctx, deps.DB, in.WsID, actorID)
		if err != nil {
			return nil, err
		}
		signalPub, err := types.Parse(in.SignalID)
		if err != nil {
			return nil, httpErr(apierrors.WsInboxNotFound)
		}

		q := deps.Queries
		_, err = q.GetInboxSignalForIntake(ctx, generated.GetInboxSignalForIntakeParams{
			WorkspaceID: workspaceID,
			PublicID:    signalPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsInboxNotFound, apierrors.InternalUnexpected))
		}
		var preview sourceintake.Detail

		var requestedTask types.PublicID
		if in.Body != nil && in.Body.ExistingTaskID != "" {
			targetPub, parseErr := types.Parse(in.Body.ExistingTaskID)
			if parseErr != nil {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			requestedTask = targetPub
		}

		pub := types.New()
		var (
			response      CaptureAsIntakeBody
			answered      error
			created       bool
			linkedTaskID  sql.NullInt32
			linkedTaskPub string
		)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: workspaceID}
		err = dbretry.InTx(ctx, deps.DB, "inbox.CaptureAsIntake", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			created = false
			linkedTaskID = sql.NullInt32{}
			linkedTaskPub = ""
			qtx := deps.Queries.WithTx(tx.RawTx())

			// Lock the signal before the unique intake insert. Concurrent capture
			// requests therefore serialize around the signal and the second one
			// observes the first one's source row without appending another event.
			lockedSignal, err := qtx.GetInboxSignalForIntake(ctx, generated.GetInboxSignalForIntakeParams{
				WorkspaceID: workspaceID,
				PublicID:    signalPub,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.WsInboxNotFound)
					return errCaptureAnswered
				}
				return err
			}
			preview = sourceintake.Project(string(lockedSignal.Source), lockedSignal.Kind, lockedSignal.PayloadJson)
			body := sql.NullString{String: preview.Body, Valid: preview.Body != ""}
			sourceURL := sql.NullString{String: preview.SourceURL, Valid: preview.SourceURL != ""}
			signalID := sql.NullInt32{Int32: int32(lockedSignal.ID), Valid: true} //#nosec G115 -- signals.id is INT UNSIGNED and fits int32 in supported deployments

			_, err = qtx.CreateIntakeItemForSignal(ctx, generated.CreateIntakeItemForSignalParams{
				PublicID:    pub,
				WorkspaceID: workspaceID,
				SignalID:    signalID,
				Title:       preview.Title,
				Body:        body,
				SourceUrl:   sourceURL,
			})
			if err != nil {
				return err
			}
			item, err := qtx.FindIntakeItemBySignal(ctx, generated.FindIntakeItemBySignalParams{
				WorkspaceID: workspaceID,
				SignalID:    signalID,
			})
			if err != nil {
				return err
			}
			// The minted public id is returned only for a newly inserted row;
			// duplicate-key replay retains the existing row's id. Comparing the
			// canonical row avoids relying on client-found-rows configuration.
			created = item.PublicID == pub
			if created {
				if err := deps.Mutations.RecordInTx(ctx, tx, actor, mutationlog.Mutation{
					EventType:    eventbus.IntakeItemCreated,
					AuditAction:  "intake.create_from_signal",
					ResourceType: "intake_item",
					ResourceID:   item.PublicID.String(),
					Payload: map[string]any{
						"intakeItemId": item.PublicID.String(),
						"signalId":     signalPub.String(),
						"source":       string(lockedSignal.Source),
						"kind":         lockedSignal.Kind,
						"title":        item.Title,
					},
					CallSite: "inbox.CaptureAsIntake",
				}); err != nil {
					return err
				}
			}

			// Without an explicit target, a still-open intake joins the task its signal names.
			targetPub := requestedTask
			if targetPub == (types.PublicID{}) && !item.TaskID.Valid && lockedSignal.TaskID.Valid &&
				sourceintake.Linkable(item.TriageStatus, item.SnoozeUntil) {
				signalTaskPub, err := qtx.FindPublicIdForIntakeTask(ctx, generated.FindPublicIdForIntakeTaskParams{
					WorkspaceID: workspaceID,
					ID:          uint32(lockedSignal.TaskID.Int32), //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
				})
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil {
					targetPub = signalTaskPub
				}
			}

			if targetPub != (types.PublicID{}) {
				target, err := sourceintake.LockLinkTarget(ctx, tx.RawTx(), workspaceID, actorID, targetPub)
				if err != nil {
					var apiError *apierr.APIError
					if errors.As(err, &apiError) {
						answered = handlerutil.HTTPErrFromAPIError(apiError)
						return errCaptureAnswered
					}
					return err
				}
				targetFK := sql.NullInt32{Int32: int32(target.Task.ID), Valid: true} //#nosec G115 -- tasks.id is INT UNSIGNED and fits int32 in supported deployments
				if lockedSignal.TaskID.Valid && lockedSignal.TaskID.Int32 != targetFK.Int32 {
					answered = httpErr(apierrors.WsTaskNotFound)
					return errCaptureAnswered
				}
				if item.TaskID.Valid {
					if item.TaskID.Int32 != targetFK.Int32 {
						answered = httpErr(apierrors.WsIntakeAlreadyConverted)
						return errCaptureAnswered
					}
				} else {
					if !sourceintake.Linkable(item.TriageStatus, item.SnoozeUntil) {
						answered = httpErr(apierrors.WsIntakeAlreadyTriaged)
						return errCaptureAnswered
					}
					updated, err := qtx.LinkIntakeItemToExistingTask(ctx, generated.LinkIntakeItemToExistingTaskParams{
						TaskID:          targetFK,
						TriagedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor id is INT UNSIGNED and fits int32 in supported deployments
						WorkspaceID:     workspaceID,
						PublicID:        item.PublicID,
					})
					if err != nil {
						return err
					}
					if updated == 0 {
						answered = httpErr(apierrors.WsIntakeAlreadyConverted)
						return errCaptureAnswered
					}
					if err := deps.Mutations.RecordInTx(ctx, tx, actor, mutationlog.Mutation{
						EventType:    eventbus.IntakeItemAccepted,
						AuditAction:  "intake.link_existing_task",
						ResourceType: "intake_item",
						ResourceID:   item.PublicID.String(),
						TaskID:       int64Ref(int64(target.Task.ID)),
						Payload:      sourceintake.AcceptedPayload(item.PublicID, target.PublicID, target.ProjectID, signalPub),
						CallSite:     "inbox.CaptureAsIntake",
					}); err != nil {
						return err
					}
				}
				if !lockedSignal.TaskID.Valid {
					updatedSignal, err := qtx.LinkInboxSignalToTask(ctx, generated.LinkInboxSignalToTaskParams{
						TaskID:      targetFK,
						WorkspaceID: workspaceID,
						ID:          lockedSignal.ID,
					})
					if err != nil {
						return err
					}
					if updatedSignal == 0 {
						answered = httpErr(apierrors.WsTaskNotFound)
						return errCaptureAnswered
					}
					if err := deps.Mutations.RecordInTx(ctx, tx, actor, mutationlog.Mutation{
						EventType:    eventbus.SignalAttached,
						AuditAction:  "signal.attach",
						ResourceType: "signal",
						ResourceID:   lockedSignal.PublicID.String(),
						TaskID:       int64Ref(int64(target.Task.ID)),
						Payload: map[string]any{
							"signalId": lockedSignal.PublicID.String(),
							"taskId":   target.PublicID.String(),
							"source":   string(lockedSignal.Source),
							"kind":     lockedSignal.Kind,
						},
						CallSite: "inbox.CaptureAsIntake",
					}); err != nil {
						return err
					}
				}
				linkedTaskID = targetFK
				linkedTaskPub = target.PublicID.String()
			} else if item.TaskID.Valid {
				linkedTaskID = item.TaskID
			}

			response = CaptureAsIntakeBody{
				IntakeItemID: item.PublicID.String(),
				Title:        item.Title,
				Body:         nullStr(item.Body),
				SourceURL:    nullStr(item.SourceUrl),
				Created:      created,
			}
			return nil
		})
		if errors.Is(err, errCaptureAnswered) {
			return nil, answered
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// A pre-existing conversion may have been made by a different member,
		// so its id is returned only under the caller's current visibility.
		if linkedTaskID.Valid && linkedTaskPub == "" {
			taskPub, err := sourceintake.LinkedTask(ctx, deps.DB, deps.Queries, workspaceID, actorID, linkedTaskID.Int32, apierrors.WsTaskAccessDenied)
			if err != nil {
				var apiError *apierr.APIError
				if errors.As(err, &apiError) {
					return nil, handlerutil.HTTPErrFromAPIError(apiError)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			linkedTaskPub = taskPub.String()
		}
		response.TaskID = linkedTaskPub
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		return &CaptureAsIntakeOutput{Status: status, Body: response}, nil
	}
}

var errCaptureAnswered = errors.New("inbox.CaptureAsIntake: answered")

func int64Ref(v int64) *int64 { return &v }
