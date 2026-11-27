package taskservice

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
)

// MaxDecisionReasonRunes bounds a rejection reason.
const MaxDecisionReasonRunes = 500

// Verdicts a human gives a proposal.
const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
)

// Decision is one human verdict on a proposal.
type Decision struct {
	Verdict string
	// Reason is stored with a rejection; at most 500 runes.
	Reason string
}

// DecideStatus is the closed set of results of Decide.
type DecideStatus string

const (
	DecideApplied  DecideStatus = "applied"
	DecideRejected DecideStatus = "rejected"
	DecideExpired  DecideStatus = "expired"
	DecideStale    DecideStatus = "stale"
	DecideFailed   DecideStatus = "failed"
	DecidePaused   DecideStatus = "paused"
)

// DecideResult is what Decide committed. ErrorCode names the reason of a
// stale or failed result.
type DecideResult struct {
	Status    DecideStatus
	ErrorCode string
}

// ErrDecision is returned for a verdict or reason the caller should have
// rejected at the boundary.
var ErrDecision = errors.New("taskservice: decision needs verdict approve or reject and a reason of at most 500 characters")

// decideRollback carries an apply refusal out so the transaction rolls back.
type decideRollback struct{ fail *applyFailure }

func (r *decideRollback) Error() string { return r.fail.Error() }

// proposalEffectiveStatus is the status a reader sees: a pending proposal
// past its expiry reads as expired before anything writes that.
func proposalEffectiveStatus(status generated.AgentActionProposalsStatus, expiresAt, now time.Time) generated.AgentActionProposalsStatus {
	if status == generated.AgentActionProposalsStatusPending && !now.Before(expiresAt) {
		return generated.AgentActionProposalsStatusExpired
	}
	return status
}

// Decide approves or rejects one pending proposal. Expired, stale and
// paused results are committed (a status write or nothing) and then
// returned as their API error; an apply refusal rolls the approval back
// and records stale or failed in a second transaction.
func (s *Service) Decide(ctx context.Context, approver execution.Principal, task, proposal types.PublicID, d Decision) (DecideResult, error) {
	if err := s.checkReadiness(true); err != nil {
		return DecideResult{}, err
	}
	if approver.IsAgent() || approver.Validate() != nil {
		return DecideResult{}, execution.ErrInvalidPrincipal
	}
	if (d.Verdict != VerdictApprove && d.Verdict != VerdictReject) ||
		!utf8.ValidString(d.Reason) || utf8.RuneCountInString(d.Reason) > MaxDecisionReasonRunes {
		return DecideResult{}, ErrDecision
	}
	ws := approver.WorkspaceID()

	// The kind and project are read outside the transaction: a subtask
	// approval must take the project lock as its first statement.
	rec, err := acl.ResolveTaskByPublicID(ctx, s.DB, uuid.UUID(task.UUID()))
	if err != nil {
		return DecideResult{}, err
	}
	if rec.WorkspaceID != ws {
		return DecideResult{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	pre, err := s.Queries.GetAgentActionProposalForUpdate(ctx, generated.GetAgentActionProposalForUpdateParams{
		WorkspaceID: ws, TaskID: rec.ID, PublicID: proposal,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return DecideResult{}, apierrors.New(apierrors.WsAgentProposalNotFound)
	}
	if err != nil {
		return DecideResult{}, err
	}
	kind := execution.Tool(pre.Kind)

	var out DecideResult
	err = dbretry.InTx(ctx, s.DB, "taskservice.Decide", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		r, err := s.decideTx(ctx, tx, approver, task, proposal, kind, rec.ProjectID, d)
		out = r
		return err
	})
	var rb *decideRollback
	if errors.As(err, &rb) {
		return s.recordApplyFailure(ctx, approver, rec.ID, proposal, rb.fail)
	}
	if err != nil {
		return DecideResult{}, err
	}
	switch out.Status {
	case DecideExpired:
		return out, apierrors.New(apierrors.WsAgentProposalExpired)
	case DecideStale:
		return out, apierrors.New(apierrors.WsAgentProposalStale)
	case DecidePaused:
		return out, apierrors.New(apierrors.AiAgentPaused)
	}
	return out, nil
}

func (s *Service) decideTx(ctx context.Context, tx *dbretry.Tx, approver execution.Principal, task, proposal types.PublicID, kind execution.Tool, projectID uint32, d Decision) (DecideResult, error) {
	ws := approver.WorkspaceID()
	if kind == execution.ToolCreateSubtask {
		if err := taskcreate.LockProject(ctx, tx, ws, projectID); err != nil {
			return DecideResult{}, err
		}
	}
	authz, err := execution.AuthorizeTaskTx(ctx, tx, approver, task, execution.CapabilityReadTask)
	if errors.Is(err, execution.ErrTaskOutsideWorkspace) {
		return DecideResult{}, apierrors.New(apierrors.WsTaskNotFound)
	}
	if err != nil {
		return DecideResult{}, err
	}
	qtx := s.Queries.WithTx(tx.RawTx())
	row, err := qtx.GetAgentActionProposalForUpdate(ctx, generated.GetAgentActionProposalForUpdateParams{
		WorkspaceID: ws, TaskID: authz.Task.ID, PublicID: proposal,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return DecideResult{}, apierrors.New(apierrors.WsAgentProposalNotFound)
	}
	if err != nil {
		return DecideResult{}, err
	}
	kind = execution.Tool(row.Kind)
	if _, err := execution.AuthorizeTaskTx(ctx, tx, approver, task, kind.Capability()); err != nil {
		return DecideResult{}, err
	}
	if row.Status != generated.AgentActionProposalsStatusPending {
		return DecideResult{}, apierrors.New(apierrors.WsAgentProposalAlreadyDecided)
	}
	now := time.Now().UTC()
	if proposalEffectiveStatus(row.Status, row.ExpiresAt, now) == generated.AgentActionProposalsStatusExpired {
		if err := setProposalStatus(ctx, qtx, ws, row.ID, generated.AgentActionProposalsStatusExpired, 0, now, "", ""); err != nil {
			return DecideResult{}, err
		}
		return DecideResult{Status: DecideExpired}, nil
	}
	approverID := approver.UserID()
	decided := decidedEvent{ws: ws, taskID: authz.Task.ID, task: task, row: row, approverID: approverID}

	if d.Verdict == VerdictReject {
		if err := setProposalStatus(ctx, qtx, ws, row.ID, generated.AgentActionProposalsStatusRejected, approverID, now, d.Reason, ""); err != nil {
			return DecideResult{}, err
		}
		decided.status, decided.reason, decided.audit = DecideRejected, d.Reason, "ai_agent.proposal.reject"
		return DecideResult{Status: DecideRejected}, s.recordDecided(ctx, tx, decided)
	}

	stale := func(spec *apierrors.Spec) (DecideResult, error) {
		if err := setProposalStatus(ctx, qtx, ws, row.ID, generated.AgentActionProposalsStatusStale, approverID, now, "", spec.Code); err != nil {
			return DecideResult{}, err
		}
		return DecideResult{Status: DecideStale, ErrorCode: spec.Code}, nil
	}
	if row.AgentEnabled && row.AgentPaused {
		return DecideResult{Status: DecidePaused}, nil
	}
	if !row.AgentEnabled {
		return stale(apierrors.WsTaskAgentNotAssigned)
	}
	current, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{WorkspaceID: ws, PublicID: task})
	if err != nil {
		return DecideResult{}, err
	}
	if !taskWorkable(current) {
		return stale(apierrors.WsTaskAgentTaskNotWorkable)
	}
	agent, err := execution.NewAgentPrincipal(ws, row.AgentID, []string{execution.ScopeWriteWorkspace}, []string{string(kind)}, []types.PublicID{task}, 0)
	if err != nil {
		return DecideResult{}, err
	}
	agentAuthz, err := execution.AuthorizeTaskTx(ctx, tx, agent, task, kind.Capability())
	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) && (apiErr.Spec == apierrors.WsTaskNotFound || apiErr.Spec == apierrors.McpScopeInsufficient) {
		return stale(apierrors.WsTaskAgentNotAssigned)
	}
	if err != nil {
		return DecideResult{}, err
	}
	if kind == execution.ToolCreateSubtask && agentAuthz.Task.ProjectID != projectID {
		return stale(apierrors.WsTaskUpdateConflict)
	}
	cond, err := decodePrecondition(row.PreconditionJson)
	if err != nil {
		return DecideResult{}, err
	}
	if kind == execution.ToolTransitionTask {
		latest, err := latestTransitionEventID(ctx, qtx, ws, authz.Task.ID)
		if err != nil {
			return DecideResult{}, err
		}
		if latest != cond.before.TransitionEventID {
			return stale(apierrors.WsTaskUpdateConflict)
		}
	}
	expect, err := cond.expectationFor(kind)
	if err != nil {
		return DecideResult{}, err
	}
	action, _, err := decodeProposalArgs(row.Kind, row.ArgsJson)
	if err != nil {
		return DecideResult{}, err
	}
	action.ProjectID = agentAuthz.Task.ProjectID
	approverRow, err := qtx.LockCommentHumanAuthor(ctx, approverID)
	if err != nil {
		return DecideResult{}, err
	}
	err = s.apply(ctx, tx, applyArgs{
		WorkspaceID:  ws,
		TaskID:       authz.Task.ID,
		TaskPublicID: task,
		ProjectID:    agentAuthz.Task.ProjectID,
		Visibility:   current.Visibility,
		Agent:        *agentAuthz.Agent,
		Action:       action,
		Expect:       expect,
		Extra: map[string]any{
			"approvedByUserId": approverRow.PublicID.String(),
			"proposalId":       proposal.String(),
			"runKey":           row.RunKey,
		},
	})
	var fail *applyFailure
	if errors.As(err, &fail) {
		return DecideResult{}, &decideRollback{fail: fail}
	}
	if err != nil {
		return DecideResult{}, err
	}
	if err := setProposalStatus(ctx, qtx, ws, row.ID, generated.AgentActionProposalsStatusApplied, approverID, now, "", ""); err != nil {
		return DecideResult{}, err
	}
	decided.status, decided.audit = DecideApplied, "ai_agent.proposal.approve"
	return DecideResult{Status: DecideApplied}, s.recordDecided(ctx, tx, decided)
}

// setProposalStatus claims a pending proposal. The caller holds the row
// lock and has checked pending, so a lost claim is a broken invariant.
func setProposalStatus(ctx context.Context, qtx *generated.Queries, ws, id uint32, status generated.AgentActionProposalsStatus, decidedBy uint32, at time.Time, reason, code string) error {
	n, err := qtx.UpdateAgentActionProposalStatus(ctx, generated.UpdateAgentActionProposalStatusParams{
		Status:          status,
		DecidedByUserID: optionalUser(decidedBy),
		DecidedAt:       sql.NullTime{Time: at, Valid: true},
		DecisionReason:  sql.NullString{String: reason, Valid: reason != ""},
		ErrorCode:       sql.NullString{String: code, Valid: code != ""},
		WorkspaceID:     ws,
		ID:              id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return apierrors.New(apierrors.WsAgentProposalAlreadyDecided)
	}
	return nil
}

type decidedEvent struct {
	ws         uint32
	taskID     uint32
	task       types.PublicID
	row        generated.GetAgentActionProposalForUpdateRow
	approverID uint32
	status     DecideStatus
	reason     string
	audit      string
}

func (s *Service) recordDecided(ctx context.Context, tx *dbretry.Tx, e decidedEvent) error {
	taskID := internalTaskID(e.taskID)
	payload := map[string]any{
		"taskId":     e.task.String(),
		"proposalId": e.row.PublicID.String(),
		"agentId":    e.row.AgentPublicID.String(),
		"kind":       string(e.row.Kind),
		"status":     string(e.status),
	}
	if e.reason != "" {
		payload["reason"] = e.reason
	}
	return s.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: e.approverID, WorkspaceID: e.ws}, mutationlog.Mutation{
		EventType:    eventbus.AgentActionDecided,
		AuditAction:  e.audit,
		ResourceType: "agent_action_proposal",
		ResourceID:   e.row.PublicID.String(),
		TaskID:       &taskID,
		Payload:      payload,
		CallSite:     "taskservice.Decide",
	})
}

// recordApplyFailure marks the proposal stale (a conflict) or failed in its
// own transaction after the approval rolled back.
func (s *Service) recordApplyFailure(ctx context.Context, approver execution.Principal, taskID uint32, proposal types.PublicID, fail *applyFailure) (DecideResult, error) {
	status, result := generated.AgentActionProposalsStatusFailed, DecideResult{Status: DecideFailed, ErrorCode: fail.spec.Code}
	if fail.kind == failConflict {
		status, result.Status = generated.AgentActionProposalsStatusStale, DecideStale
	}
	err := dbretry.InTx(ctx, s.DB, "taskservice.Decide.failure", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		qtx := s.Queries.WithTx(tx.RawTx())
		row, err := qtx.GetAgentActionProposalForUpdate(ctx, generated.GetAgentActionProposalForUpdateParams{
			WorkspaceID: approver.WorkspaceID(), TaskID: taskID, PublicID: proposal,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Status != generated.AgentActionProposalsStatusPending {
			return nil
		}
		return setProposalStatus(ctx, qtx, approver.WorkspaceID(), row.ID, status, approver.UserID(), time.Now().UTC(), "", fail.spec.Code)
	})
	if err != nil {
		return DecideResult{}, err
	}
	if result.Status == DecideStale {
		return result, apierrors.Wrap(apierrors.WsAgentProposalStale, fail)
	}
	return result, apierrors.Wrap(fail.spec, fail.cause)
}

// ProposalFilter selects proposals for ListProposals.
type ProposalFilter struct {
	// Status is "pending" (every live pending proposal) or "all".
	Status string
	// Limit applies to "all": default 20, at most 100.
	Limit int
}

// Proposal status filters.
const (
	ProposalFilterPending = "pending"
	ProposalFilterAll     = "all"
)

// ProposalUser names the human who decided a proposal.
type ProposalUser struct {
	ID          types.PublicID
	DisplayName string
}

// Proposal is one agent proposal as a task viewer sees it.
type Proposal struct {
	ID             types.PublicID
	AgentID        types.PublicID
	AgentName      string
	Kind           string
	Args           ProposalArgs
	Before         ProposalBefore
	Status         string
	ErrorCode      string
	DecisionReason string
	DecidedBy      *ProposalUser
	DecidedAt      *time.Time
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// ErrProposalFilter is returned for a filter the boundary should have refused.
var ErrProposalFilter = errors.New("taskservice: proposal filter needs status pending or all and a limit within 0..100")

// pendingListCap bounds the pending list; each agent holds at most
// MaxPendingProposals live ones, so the cap is never the binding limit in
// practice.
const pendingListCap = 1000

// ListProposals returns a task's proposals to anyone who can read the task.
// A pending proposal past its expiry is shown as expired and is left out
// of the pending list.
func (s *Service) ListProposals(ctx context.Context, viewer execution.Principal, task types.PublicID, f ProposalFilter) ([]Proposal, error) {
	if err := s.checkReadiness(false); err != nil {
		return nil, err
	}
	if f.Limit < 0 || f.Limit > 100 || (f.Status != ProposalFilterPending && f.Status != ProposalFilterAll) {
		return nil, ErrProposalFilter
	}
	params := generated.ListProposalsForTaskParams{WorkspaceID: viewer.WorkspaceID(), Limit: 20}
	if f.Limit > 0 {
		params.Limit = int32(f.Limit) //#nosec G115 -- bounded to 100 above
	}
	if f.Status == ProposalFilterPending {
		params.Status = generated.NullAgentActionProposalsStatus{AgentActionProposalsStatus: generated.AgentActionProposalsStatusPending, Valid: true}
		params.Limit = pendingListCap
	}
	var rows []generated.ListProposalsForTaskRow
	err := dbretry.InTx(ctx, s.DB, "taskservice.ListProposals", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		authz, err := execution.AuthorizeTaskTx(ctx, tx, viewer, task, execution.CapabilityReadTask)
		if errors.Is(err, execution.ErrTaskOutsideWorkspace) {
			return apierrors.New(apierrors.WsTaskNotFound)
		}
		if err != nil {
			return err
		}
		params.TaskID = authz.Task.ID
		rows, err = s.Queries.WithTx(tx.RawTx()).ListProposalsForTask(ctx, params)
		return err
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]Proposal, 0, len(rows))
	for _, r := range rows {
		status := proposalEffectiveStatus(r.Status, r.ExpiresAt, now)
		if f.Status == ProposalFilterPending && status != generated.AgentActionProposalsStatusPending {
			continue
		}
		_, args, err := decodeProposalArgs(r.Kind, r.ArgsJson)
		if err != nil {
			return nil, err
		}
		cond, err := decodePrecondition(r.PreconditionJson)
		if err != nil {
			return nil, err
		}
		p := Proposal{
			ID:             r.PublicID,
			AgentID:        r.AgentPublicID,
			AgentName:      r.AgentName,
			Kind:           string(r.Kind),
			Args:           args,
			Before:         cond.before,
			Status:         string(status),
			ErrorCode:      r.ErrorCode.String,
			DecisionReason: r.DecisionReason.String,
			CreatedAt:      r.CreatedAt,
			ExpiresAt:      r.ExpiresAt,
		}
		if r.UserPublicID != (types.PublicID{}) {
			p.DecidedBy = &ProposalUser{ID: r.UserPublicID, DisplayName: r.DecidedByDisplayName.String}
		}
		if r.DecidedAt.Valid {
			at := r.DecidedAt.Time
			p.DecidedAt = &at
		}
		out = append(out, p)
	}
	return out, nil
}
