package suggestioncommand

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/inboxsnapshot"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/operation"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

const (
	operationKindExecute = "ai.suggestion.execute"
	operationTargetKind  = "inbox_item"
	maxReasoningRunes    = 2_000
	maxSnoozeDuration    = 30 * 24 * time.Hour
	defaultSnooze        = time.Hour
	decisionEditLifetime = 30 * 24 * time.Hour
	decisionHideLifetime = 7 * 24 * time.Hour
)

// Service coordinates suggestion decisions and execution with the shared
// operation receipt table and event log.
type Service struct {
	DB                *sql.DB
	Mutations         *mutationlog.Recorder
	SuggestionPrivacy *suggestionprivacy.Store
}

// Execute performs one recommendation action and commits its effect, audit
// events and operation receipt in the same transaction. Open only returns a
// currently authorized navigation target; it does not resolve the suggestion.
func (s *Service) Execute(ctx context.Context, in ExecuteCommand) (ExecuteResult, error) {
	if s == nil || s.DB == nil || s.Mutations == nil {
		return ExecuteResult{}, apierrors.New(apierrors.InternalUnexpected)
	}
	reasoning, err := normalizeReasoning(in.Reasoning)
	if err != nil {
		return ExecuteResult{}, err
	}
	in.Reasoning = reasoning
	if !validAction(in.Action) || in.WorkspaceID == 0 || in.WorkspacePublicID == (types.PublicID{}) ||
		in.ActorUserID == 0 || in.InboxItemID == (types.PublicID{}) || in.OperationKey == "" ||
		!utf8.ValidString(in.OperationKey) || utf8.RuneCountInString(in.OperationKey) > 128 ||
		(in.Action != "snooze" && in.SnoozeUntil != nil) {
		return ExecuteResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.Action == "snooze" && in.SnoozeUntil != nil && !validSnoozeDeadline(time.Now().UTC(), *in.SnoozeUntil) {
		return ExecuteResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}

	requestHash, err := executeRequestHash(in)
	if err != nil {
		return ExecuteResult{}, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	var result ExecuteResult
	txErr := dbretry.InTx(ctx, s.DB, "suggestioncommand.Execute", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		result = ExecuteResult{}
		q := generated.New(tx.RawTx())
		receipt, err := operation.Lock(ctx, tx, operation.Request{
			WorkspaceID:    in.WorkspaceID,
			Principal:      operation.Principal{Kind: operation.PrincipalUser, ID: in.ActorUserID},
			Kind:           operationKindExecute,
			Key:            in.OperationKey,
			TargetKind:     operationTargetKind,
			TargetPublicID: in.InboxItemID,
			Hash:           requestHash,
		})
		if err != nil {
			if errors.Is(err, operation.ErrInvalidRequest) {
				return apierrors.New(apierrors.ValidationBodyFieldInvalid)
			}
			return apierrors.Wrap(apierrors.InternalUnexpected, err)
		}

		role, err := lockWorkspaceRole(ctx, q, in.WorkspacePublicID, in.ActorUserID)
		if err != nil {
			return err
		}
		signal, task, err := lockAuthorizedTarget(ctx, q, in.WorkspaceID, in.ActorUserID, role, in.InboxItemID)
		if err != nil {
			return err
		}
		if in.Action != "open" && !role.AtLeast(acl.WorkspaceRoleMember) {
			return apierrors.New(apierrors.WsMemberRoleDenied)
		}

		if receipt.Hash != requestHash {
			return apierrors.New(apierrors.AiOperationKeyConflict)
		}
		if receipt.CompletedAt != nil {
			if err := json.Unmarshal(receipt.Result, &result); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			result.Replayed = true
			return nil
		}

		if !signal.Enabled {
			return apierrors.New(apierrors.AiSuggestionAlreadyResolved)
		}
		proposal, err := resolveProposal(ctx, q, in.WorkspaceID, in.ActorUserID, in.InboxItemID, in.ProposalID)
		if err != nil {
			return err
		}
		if err := s.authorizeProposalBatch(ctx, q, in.WorkspaceID, in.ActorUserID, proposal); err != nil {
			return err
		}
		if err := validateProposalEvidence(ctx, q, in.WorkspaceID, signal, task, proposal); err != nil {
			return err
		}
		decision, hasDecision, err := readDecision(ctx, q, in.WorkspaceID, proposal.EventID, in.ActorUserID)
		if err != nil {
			return err
		}
		revision := uint32(0)
		if hasDecision {
			revision = decision.Revision
		}
		if in.ExpectedRevision != revision {
			return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
		}
		if err := validateActiveDecision(decision, hasDecision, time.Now().UTC(), proposal, in.Action, in.Reasoning); err != nil {
			return err
		}

		now := time.Now().UTC()
		result = ExecuteResult{
			OperationID: receipt.ID.String(),
			ProposalID:  proposal.PublicID.String(),
			Revision:    revision,
			Action:      in.Action,
			Status:      "executed",
			InboxItemID: in.InboxItemID.String(),
			Replayed:    false,
		}
		if task != nil {
			result.TaskID = task.PublicID.String()
		}

		switch in.Action {
		case "archive":
			rows, err := q.ArchiveInboxItem(ctx, generated.ArchiveInboxItemParams{
				WorkspaceID: in.WorkspaceID,
				PublicID:    in.InboxItemID,
			})
			if err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			if rows != 1 {
				return apierrors.New(apierrors.AiSuggestionAlreadyResolved)
			}
			if err := s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: in.ActorUserID, WorkspaceID: in.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.SignalArchived,
				AuditAction:  "inbox.archive",
				ResourceType: "inbox_item",
				ResourceID:   in.InboxItemID.String(),
				Payload: map[string]any{
					"inboxItemId": in.InboxItemID.String(),
				},
				CallSite: "suggestioncommand.Execute.archive",
			}); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			at := now.Unix()
			result.ExecutedAt = &at
		case "snooze":
			until := now.Add(defaultSnooze)
			if in.SnoozeUntil != nil {
				until = time.Unix(*in.SnoozeUntil, 0).UTC()
			}
			if !validSnoozeTime(now, until) {
				return apierrors.New(apierrors.ValidationBodyFieldInvalid)
			}
			if _, err := q.SnoozeInboxItem(ctx, generated.SnoozeInboxItemParams{
				ReceivedAt:  until,
				WorkspaceID: in.WorkspaceID,
				PublicID:    in.InboxItemID,
			}); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			untilUnix := until.Unix()
			result.SnoozeUntil = &untilUnix
			if err := s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: in.ActorUserID, WorkspaceID: in.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.SignalSnoozed,
				AuditAction:  "inbox.snooze",
				ResourceType: "inbox_item",
				ResourceID:   in.InboxItemID.String(),
				Payload: map[string]any{
					"inboxItemId": in.InboxItemID.String(),
					"snoozeUntil": untilUnix,
				},
				CallSite: "suggestioncommand.Execute.snooze",
			}); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			at := now.Unix()
			result.ExecutedAt = &at
		case "open":
			if err := checkActionTarget(in.Action, task); err != nil {
				return err
			}
			result.Status = "navigation_ready"
		}

		if in.Action != "open" {
			if err := s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: in.ActorUserID, WorkspaceID: in.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.AiSuggestionApplied,
				AuditAction:  "ai.suggestion.apply",
				ResourceType: "inbox_item",
				ResourceID:   in.InboxItemID.String(),
				Payload: map[string]any{
					"inbox_item_id": in.InboxItemID.String(),
					"proposal_id":   proposal.PublicID.String(),
					"revision":      revision,
					"action":        in.Action,
					"operation_id":  receipt.ID.String(),
				},
				CallSite: "suggestioncommand.Execute.applied",
			}); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
		}

		encoded, err := json.Marshal(result)
		if err != nil {
			return apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		if err := operation.Complete(ctx, tx, receipt, encoded); err != nil {
			return apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		return nil
	})
	if txErr != nil {
		var apiErr *apierr.APIError
		if errors.As(txErr, &apiErr) {
			return ExecuteResult{}, apiErr
		}
		return ExecuteResult{}, apierrors.Wrap(apierrors.InternalUnexpected, txErr)
	}
	return result, nil
}

// Decide persists an actor-specific edit, dismissal, or deferral. The signal
// lock serializes revisions and shares the lock order with Execute.
func (s *Service) Decide(ctx context.Context, in DecisionCommand) (DecisionResult, error) {
	if s == nil || s.DB == nil || s.Mutations == nil {
		return DecisionResult{}, apierrors.New(apierrors.InternalUnexpected)
	}
	var err error
	in.Reasoning, err = normalizeReasoning(in.Reasoning)
	if err != nil {
		return DecisionResult{}, err
	}
	if in.WorkspaceID == 0 || in.WorkspacePublicID == (types.PublicID{}) || in.ActorUserID == 0 ||
		in.InboxItemID == (types.PublicID{}) || in.ProposalID == (types.PublicID{}) ||
		(in.State != "edited" && in.State != "dismissed" && in.State != "deferred") {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.State == "edited" && (!validAction(in.Action) || in.Reasoning == "") {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.State != "edited" && (in.Action != "" || in.Reasoning != "") {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.State == "deferred" && in.DeferUntil == nil {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.State != "deferred" && in.DeferUntil != nil {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	if in.DeferUntil != nil && !validSnoozeDeadline(time.Now().UTC(), *in.DeferUntil) {
		return DecisionResult{}, apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}

	var result DecisionResult
	txErr := dbretry.InTx(ctx, s.DB, "suggestioncommand.Decide", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		result = DecisionResult{}
		q := generated.New(tx.RawTx())
		role, err := lockWorkspaceRole(ctx, q, in.WorkspacePublicID, in.ActorUserID)
		if err != nil {
			return err
		}
		if !role.AtLeast(acl.WorkspaceRoleMember) {
			return apierrors.New(apierrors.WsMemberRoleDenied)
		}
		signal, task, err := lockAuthorizedTarget(ctx, q, in.WorkspaceID, in.ActorUserID, role, in.InboxItemID)
		if err != nil {
			return err
		}
		if !signal.Enabled {
			return apierrors.New(apierrors.AiSuggestionAlreadyResolved)
		}
		proposal, err := resolveProposal(ctx, q, in.WorkspaceID, in.ActorUserID, in.InboxItemID, &in.ProposalID)
		if err != nil {
			return err
		}
		if err := s.authorizeProposalBatch(ctx, q, in.WorkspaceID, in.ActorUserID, proposal); err != nil {
			return err
		}
		if err := validateProposalEvidence(ctx, q, in.WorkspaceID, signal, task, proposal); err != nil {
			return err
		}
		if in.State == "edited" {
			if err := checkActionTarget(in.Action, task); err != nil {
				return err
			}
		}
		decision, hasDecision, err := readDecision(ctx, q, in.WorkspaceID, proposal.EventID, in.ActorUserID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		currentRevision := uint32(0)
		if hasDecision {
			currentRevision = decision.Revision
		}
		if in.ExpectedRevision != currentRevision {
			// A retry whose persisted decision already matches returns it unchanged.
			if hasDecision && decisionMatches(decision, in) && decision.SourceVersion.Valid && bytes.Equal([]byte(decision.SourceVersion.String), proposal.SourceVersion) {
				result = decisionResult(decision, proposal.PublicID)
				return nil
			}
			return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
		}
		if err := checkNoProposalReaction(ctx, q, in.WorkspaceID, in.InboxItemID, proposal); err != nil {
			return err
		}

		expiresAt := now.Add(decisionEditLifetime)
		state := generated.AiSuggestionDecisionsState(in.State)
		action := generated.NullAiSuggestionDecisionsAction{}
		reasoning := sql.NullString{}
		switch in.State {
		case "edited":
			action = generated.NullAiSuggestionDecisionsAction{AiSuggestionDecisionsAction: generated.AiSuggestionDecisionsAction(in.Action), Valid: true}
			reasoning = sql.NullString{String: in.Reasoning, Valid: true}
		case "dismissed":
			expiresAt = now.Add(decisionHideLifetime)
		default:
			expiresAt = time.Unix(*in.DeferUntil, 0).UTC()
		}
		sourceVersion := sql.NullString{String: string(proposal.SourceVersion), Valid: true}
		if hasDecision {
			rows, err := q.UpdateSuggestionDecision(ctx, generated.UpdateSuggestionDecisionParams{
				State:         state,
				Action:        action,
				Reasoning:     reasoning,
				SourceVersion: sourceVersion,
				ExpiresAt:     expiresAt,
				ID:            decision.ID,
				WorkspaceID:   in.WorkspaceID,
				UserID:        in.ActorUserID,
			})
			if err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
			if rows != 1 {
				return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
			}
		} else if err := q.CreateSuggestionDecision(ctx, generated.CreateSuggestionDecisionParams{
			PublicID:        types.New(),
			WorkspaceID:     in.WorkspaceID,
			ProposalEventID: proposal.EventID,
			UserID:          in.ActorUserID,
			State:           state,
			Action:          action,
			Reasoning:       reasoning,
			SourceVersion:   sourceVersion,
			ExpiresAt:       expiresAt,
		}); err != nil {
			return apierrors.Wrap(apierrors.InternalUnexpected, err)
		}

		nextRevision := currentRevision + 1
		if err := s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: in.ActorUserID, WorkspaceID: in.WorkspaceID}, mutationlog.Mutation{
			EventType:    eventbus.AiSuggestionEdited,
			AuditAction:  "ai.suggestion.edit",
			ResourceType: "inbox_item",
			ResourceID:   in.InboxItemID.String(),
			Payload: map[string]any{
				"inbox_item_id":       in.InboxItemID.String(),
				"proposal_id":         proposal.PublicID.String(),
				"suggestion_batch_id": proposal.BatchID.String(),
				"decision_scope":      "actor",
				"revision":            nextRevision,
				"state":               in.State,
				"action":              in.Action,
				"reasoning":           in.Reasoning,
			},
			CallSite: "suggestioncommand.Decide",
		}); err != nil {
			return apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		if in.State == "dismissed" {
			if err := s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: in.ActorUserID, WorkspaceID: in.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.AiSuggestionDismissed,
				AuditAction:  "ai.suggestion.dismiss",
				ResourceType: "inbox_item",
				ResourceID:   in.InboxItemID.String(),
				Payload: map[string]any{
					"inbox_item_id": in.InboxItemID.String(),
					"proposal_id":   proposal.PublicID.String(),
					"revision":      nextRevision,
				},
				CallSite: "suggestioncommand.Decide.dismissed",
			}); err != nil {
				return apierrors.Wrap(apierrors.InternalUnexpected, err)
			}
		}
		result = DecisionResult{
			ProposalID:        proposal.PublicID.String(),
			Revision:          nextRevision,
			State:             in.State,
			Action:            in.Action,
			Reasoning:         in.Reasoning,
			DecisionExpiresAt: expiresAt.Unix(),
		}
		return nil
	})
	if txErr != nil {
		var apiErr *apierr.APIError
		if errors.As(txErr, &apiErr) {
			return DecisionResult{}, apiErr
		}
		return DecisionResult{}, apierrors.Wrap(apierrors.InternalUnexpected, txErr)
	}
	return result, nil
}

func lockWorkspaceRole(ctx context.Context, q *generated.Queries, workspacePublicID types.PublicID, actorID uint32) (acl.WorkspaceRole, error) {
	role, err := q.GetSuggestionWorkspaceRoleForShare(ctx, generated.GetSuggestionWorkspaceRoleForShareParams{
		UserID:            actorID,
		WorkspacePublicID: workspacePublicID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", apierrors.New(apierrors.WsWorkspaceAccessDenied)
		}
		return "", apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	workspaceRole := acl.WorkspaceRole(role)
	if !workspaceRole.IsValid() {
		return "", apierrors.New(apierrors.InternalUnexpected)
	}
	return workspaceRole, nil
}

// lockAuthorizedTarget locks the signal and its linked task. The task is nil
// when absent, disabled, or unreadable by the actor, as in the triage snapshot.
func lockAuthorizedTarget(ctx context.Context, q *generated.Queries, workspaceID, actorID uint32, role acl.WorkspaceRole, inboxItemID types.PublicID) (generated.GetSuggestionSignalForUpdateRow, *generated.GetSuggestionTaskForUpdateRow, error) {
	signal, err := q.GetSuggestionSignalForUpdate(ctx, generated.GetSuggestionSignalForUpdateParams{
		WorkspaceID:    workspaceID,
		SignalPublicID: inboxItemID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.GetSuggestionSignalForUpdateRow{}, nil, apierrors.New(apierrors.WsInboxNotFound)
		}
		return generated.GetSuggestionSignalForUpdateRow{}, nil, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	if !signal.TaskID.Valid {
		return signal, nil, nil
	}
	if signal.TaskID.Int32 < 0 {
		return generated.GetSuggestionSignalForUpdateRow{}, nil, apierrors.New(apierrors.InternalUnexpected)
	}
	task, err := q.GetSuggestionTaskForUpdate(ctx, generated.GetSuggestionTaskForUpdateParams{
		WorkspaceID: workspaceID,
		TaskID:      uint32(signal.TaskID.Int32), //#nosec G115 -- negative task ids are rejected above; source is int32
	})
	if errors.Is(err, sql.ErrNoRows) {
		return signal, nil, nil
	}
	if err != nil {
		return generated.GetSuggestionSignalForUpdateRow{}, nil, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	readable, err := taskReadable(ctx, q, actorID, role, task)
	if err != nil {
		return generated.GetSuggestionSignalForUpdateRow{}, nil, err
	}
	if !readable {
		return signal, nil, nil
	}
	return signal, &task, nil
}

// checkActionTarget rejects open unless the actor can read the linked task.
func checkActionTarget(action string, task *generated.GetSuggestionTaskForUpdateRow) error {
	if action == "open" && task == nil {
		return apierrors.New(apierrors.WsInboxNotFound)
	}
	return nil
}

// taskReadable reports whether the actor may see the locked linked task,
// locking the grant that admits it.
func taskReadable(ctx context.Context, q *generated.Queries, actorID uint32, role acl.WorkspaceRole, task generated.GetSuggestionTaskForUpdateRow) (bool, error) {
	elevated := role.AtLeast(acl.WorkspaceRoleAdmin)
	switch acl.TaskVisibility(task.Visibility) {
	case acl.TaskVisibilityPublic:
		return true, nil
	case acl.TaskVisibilityProject:
		if elevated {
			return true, nil
		}
		_, err := q.GetSuggestionProjectMemberForUpdate(ctx, generated.GetSuggestionProjectMemberForUpdateParams{
			ProjectID: task.ProjectID,
			UserID:    actorID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		return true, nil
	case acl.TaskVisibilityPrivate:
		if elevated || (task.CreatedByUserID.Valid && int64(task.CreatedByUserID.Int32) == int64(actorID)) {
			return true, nil
		}
		if actorID > math.MaxInt32 {
			return false, nil
		}
		_, err := q.GetSuggestionTaskActorForUpdate(ctx, generated.GetSuggestionTaskActorForUpdateParams{
			TaskID: task.ID,
			UserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actorID is rejected above if greater than MaxInt32
		})
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		return true, nil
	default:
		return false, apierrors.New(apierrors.InternalUnexpected)
	}
}

func resolveProposal(ctx context.Context, q *generated.Queries, workspaceID, actorUserID uint32, inboxItemID types.PublicID, proposalID *types.PublicID) (parsedProposal, error) {
	itemArg := json.RawMessage(inboxItemID.String())
	var row proposalEvent
	if proposalID == nil {
		latest, err := q.FindLatestSuggestionProposalForSignal(ctx, generated.FindLatestSuggestionProposalForSignalParams{
			ActorUserID: int64(actorUserID),
			WorkspaceID: workspaceID,
			InboxItemID: itemArg,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return parsedProposal{}, apierrors.New(apierrors.AiSuggestionEvidenceChanged)
			}
			return parsedProposal{}, apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		row = proposalEvent{
			ID:          latest.ID,
			PublicID:    latest.PublicID,
			OccurredAt:  latest.OccurredAt,
			PayloadJSON: latest.PayloadJson,
		}
	} else {
		explicit, err := q.FindSuggestionProposalByPublicID(ctx, generated.FindSuggestionProposalByPublicIDParams{
			ActorUserID:      int64(actorUserID),
			WorkspaceID:      workspaceID,
			ProposalPublicID: *proposalID,
			InboxItemID:      itemArg,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return parsedProposal{}, apierrors.New(apierrors.AiSuggestionEvidenceChanged)
			}
			return parsedProposal{}, apierrors.Wrap(apierrors.InternalUnexpected, err)
		}
		row = proposalEvent{
			ID:          explicit.ID,
			PublicID:    explicit.PublicID,
			OccurredAt:  explicit.OccurredAt,
			PayloadJSON: explicit.PayloadJson,
		}
	}
	latest, err := q.FindLatestSuggestionProposalForSignal(ctx, generated.FindLatestSuggestionProposalForSignalParams{
		ActorUserID: int64(actorUserID),
		WorkspaceID: workspaceID,
		InboxItemID: itemArg,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return parsedProposal{}, apierrors.New(apierrors.AiSuggestionEvidenceChanged)
		}
		return parsedProposal{}, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	if latest.PublicID != row.PublicID {
		return parsedProposal{}, apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	proposal, err := parseProposal(row)
	if err != nil || proposal.InboxItemID != inboxItemID {
		return parsedProposal{}, apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	return proposal, nil
}

func parseProposal(row proposalEvent) (parsedProposal, error) {
	var payload struct {
		InboxItemIDSnake  string  `json:"inbox_item_id"`
		InboxItemID       string  `json:"inboxItemId"`
		ActionSnake       string  `json:"action"`
		ActionCamel       string  `json:"recommendedAction"`
		Reasoning         string  `json:"reasoning"`
		Score             float32 `json:"score"`
		SourceVersion     string  `json:"source_version"`
		SourceVersionAlt  string  `json:"sourceVersion"`
		ExpiresAt         int64   `json:"expires_at"`
		ExpiresAtAlt      int64   `json:"expiresAt"`
		SuggestionBatchID string  `json:"suggestion_batch_id"`
	}
	if err := json.Unmarshal(row.PayloadJSON, &payload); err != nil {
		return parsedProposal{}, err
	}
	itemID := payload.InboxItemIDSnake
	if itemID == "" {
		itemID = payload.InboxItemID
	}
	itemPublicID, err := types.Parse(itemID)
	if err != nil {
		return parsedProposal{}, err
	}
	action := payload.ActionSnake
	if action == "" {
		action = payload.ActionCamel
	}
	version := payload.SourceVersion
	if version == "" {
		version = payload.SourceVersionAlt
	}
	var rawVersion []byte
	if version != "" {
		rawVersion, err = hex.DecodeString(version)
		if err != nil || len(rawVersion) != sha256.Size {
			return parsedProposal{}, errors.New("invalid source version")
		}
	}
	proposal := parsedProposal{
		EventID:       row.ID,
		PublicID:      row.PublicID,
		InboxItemID:   itemPublicID,
		Action:        action,
		Reasoning:     payload.Reasoning,
		Score:         payload.Score,
		SourceVersion: rawVersion,
		HasVersion:    len(rawVersion) == sha256.Size,
		OccurredAt:    row.OccurredAt,
	}
	if payload.SuggestionBatchID != "" {
		proposal.BatchID, err = types.Parse(payload.SuggestionBatchID)
		if err != nil {
			return parsedProposal{}, err
		}
		proposal.HasBatch = true
	}
	if payload.ExpiresAt == 0 {
		payload.ExpiresAt = payload.ExpiresAtAlt
	}
	if payload.ExpiresAt > 0 && time.Now().Unix() >= payload.ExpiresAt {
		return parsedProposal{}, errors.New("proposal expired")
	}
	if !validAction(action) {
		return parsedProposal{}, errors.New("invalid proposal action")
	}
	return proposal, nil
}

func (s *Service) authorizeProposalBatch(ctx context.Context, q *generated.Queries, workspaceID, actorUserID uint32, proposal parsedProposal) error {
	if !proposal.HasBatch || s.SuggestionPrivacy == nil {
		return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	err := s.SuggestionPrivacy.Authorize(ctx, q, workspaceID, actorUserID, proposal.BatchID)
	if errors.Is(err, suggestionprivacy.ErrNotReadable) {
		return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	if err != nil {
		return apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	return nil
}

func validateProposalEvidence(ctx context.Context, q *generated.Queries, workspaceID uint32, signal generated.GetSuggestionSignalForUpdateRow, task *generated.GetSuggestionTaskForUpdateRow, proposal parsedProposal) error {
	if !proposal.HasVersion {
		return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	fields := inboxsnapshot.Fields{
		InboxItemID: proposal.InboxItemID.String(),
		Source:      string(signal.Source),
		Kind:        signal.Kind,
		Payload:     signal.PayloadJson,
		ReceivedAt:  signal.ReceivedAt,
	}
	if signal.UpdatedAt.Valid {
		fields.UpdatedAt = signal.UpdatedAt.Time
	}
	if task != nil {
		fields.TaskReadable = true
		fields.TaskPublicID = task.PublicID.String()
		fields.TaskTitle = task.Title
	}
	current, err := hex.DecodeString(inboxsnapshot.Capture(fields).VersionHash())
	if err != nil || !bytes.Equal(current, proposal.SourceVersion) {
		return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
	}
	if err := checkNoProposalReaction(ctx, q, workspaceID, signal.PublicID, proposal); err != nil {
		return err
	}
	return nil
}

func checkNoProposalReaction(ctx context.Context, q *generated.Queries, workspaceID uint32, inboxItemID types.PublicID, proposal parsedProposal) error {
	reacted, err := q.HasSuggestionProposalReaction(ctx, generated.HasSuggestionProposalReactionParams{
		WorkspaceID:      workspaceID,
		ProposalEventID:  proposal.EventID,
		InboxItemID:      json.RawMessage(inboxItemID.String()),
		ProposalPublicID: json.RawMessage(proposal.PublicID.String()),
	})
	if err != nil {
		return apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	if reacted {
		return apierrors.New(apierrors.AiSuggestionAlreadyResolved)
	}
	return nil
}

func readDecision(ctx context.Context, q *generated.Queries, workspaceID uint32, proposalEventID uint64, actorID uint32) (generated.GetSuggestionDecisionForUpdateRow, bool, error) {
	decision, err := q.GetSuggestionDecisionForUpdate(ctx, generated.GetSuggestionDecisionForUpdateParams{
		WorkspaceID:     workspaceID,
		ProposalEventID: proposalEventID,
		UserID:          actorID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generated.GetSuggestionDecisionForUpdateRow{}, false, nil
		}
		return generated.GetSuggestionDecisionForUpdateRow{}, false, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}
	return decision, true, nil
}

func validateActiveDecision(decision generated.GetSuggestionDecisionForUpdateRow, exists bool, now time.Time, proposal parsedProposal, action, reasoning string) error {
	if !exists || !decision.ExpiresAt.After(now) {
		if action != proposal.Action {
			return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
		}
		return nil
	}
	switch decision.State {
	case generated.AiSuggestionDecisionsStateDismissed, generated.AiSuggestionDecisionsStateDeferred:
		return apierrors.New(apierrors.AiSuggestionAlreadyResolved)
	case generated.AiSuggestionDecisionsStateEdited:
		if !decision.Action.Valid || string(decision.Action.AiSuggestionDecisionsAction) != action ||
			!decision.Reasoning.Valid || decision.Reasoning.String != reasoning || !decision.SourceVersion.Valid ||
			!bytes.Equal([]byte(decision.SourceVersion.String), proposal.SourceVersion) {
			return apierrors.New(apierrors.AiSuggestionEvidenceChanged)
		}
	default:
		return apierrors.New(apierrors.InternalUnexpected)
	}
	return nil
}

func executeRequestHash(in ExecuteCommand) ([32]byte, error) {
	var proposalID any
	if in.ProposalID != nil {
		proposalID = in.ProposalID.String()
	}
	canonical := struct {
		Version          int    `json:"version"`
		WorkspaceID      string `json:"workspaceId"`
		InboxItemID      string `json:"inboxItemId"`
		ProposalID       any    `json:"proposalId"`
		ExpectedRevision uint32 `json:"expectedRevision"`
		Action           string `json:"action"`
		Reasoning        string `json:"reasoning"`
		SnoozeUntil      *int64 `json:"snoozeUntil"`
	}{
		Version:          1,
		WorkspaceID:      in.WorkspacePublicID.String(),
		InboxItemID:      in.InboxItemID.String(),
		ProposalID:       proposalID,
		ExpectedRevision: in.ExpectedRevision,
		Action:           in.Action,
		Reasoning:        in.Reasoning,
		SnoozeUntil:      in.SnoozeUntil,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func normalizeReasoning(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxReasoningRunes {
		return "", apierrors.New(apierrors.ValidationBodyFieldInvalid)
	}
	return value, nil
}

func validAction(action string) bool {
	return action == "archive" || action == "snooze" || action == "open"
}

func validSnoozeDeadline(now time.Time, unix int64) bool {
	return validSnoozeTime(now, time.Unix(unix, 0).UTC())
}

func validSnoozeTime(now, until time.Time) bool {
	return until.After(now) && !until.After(now.Add(maxSnoozeDuration))
}

func decisionMatches(decision generated.GetSuggestionDecisionForUpdateRow, in DecisionCommand) bool {
	if string(decision.State) != in.State {
		return false
	}
	if in.State == "edited" {
		return decision.Action.Valid && string(decision.Action.AiSuggestionDecisionsAction) == in.Action && decision.Reasoning.Valid && decision.Reasoning.String == in.Reasoning
	}
	return !decision.Action.Valid && !decision.Reasoning.Valid
}

func decisionResult(decision generated.GetSuggestionDecisionForUpdateRow, proposalID types.PublicID) DecisionResult {
	result := DecisionResult{
		ProposalID:        proposalID.String(),
		Revision:          decision.Revision,
		State:             string(decision.State),
		DecisionExpiresAt: decision.ExpiresAt.Unix(),
	}
	if decision.Action.Valid {
		result.Action = string(decision.Action.AiSuggestionDecisionsAction)
	}
	if decision.Reasoning.Valid {
		result.Reasoning = decision.Reasoning.String
	}
	return result
}

// HashSourceVersion exposes the canonical version to producer fixtures.
func HashSourceVersion(fields inboxsnapshot.Fields) string {
	return inboxsnapshot.Capture(fields).VersionHash()
}
