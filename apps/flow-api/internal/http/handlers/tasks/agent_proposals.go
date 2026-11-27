// Agent action proposals on a task: list, approve and reject. Judgement
// (authorization, staleness, expiry, applying the change) lives in
// taskservice; these handlers resolve the caller and shape the answer.

package tasks

import (
	"context"
	"errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// AgentProposalArgs are the arguments a proposal would apply; only the
// field of the proposal's kind is set. A set_due_on proposal with no dueOn
// clears the date.
type AgentProposalArgs struct {
	Body       string  `json:"body,omitempty"`
	Priority   *int32  `json:"priority,omitempty"`
	DueOn      *string `json:"dueOn,omitempty" doc:"YYYY-MM-DD"`
	Transition string  `json:"transition,omitempty"`
	Title      string  `json:"title,omitempty"`
}

// AgentProposalBefore is the task value the proposal was made against.
type AgentProposalBefore struct {
	Priority     *int32  `json:"priority,omitempty"`
	DueOn        *string `json:"dueOn,omitempty" doc:"YYYY-MM-DD"`
	DerivedState string  `json:"derivedState,omitempty"`
}

// AgentProposalUser names the person who decided a proposal.
type AgentProposalUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AgentProposal is one operation an agent proposed on a task.
type AgentProposal struct {
	ID             string              `json:"id"`
	Agent          AgentRef            `json:"agent"`
	Kind           string              `json:"kind" enum:"add_comment,set_priority,set_due_on,transition_task,create_subtask"`
	Args           AgentProposalArgs   `json:"args"`
	Before         AgentProposalBefore `json:"before"`
	Status         string              `json:"status" enum:"pending,applied,rejected,stale,expired,failed"`
	ErrorCode      string              `json:"errorCode,omitempty"`
	DecisionReason string              `json:"decisionReason,omitempty"`
	DecidedBy      *AgentProposalUser  `json:"decidedBy,omitempty"`
	DecidedAt      *int64              `json:"decidedAt,omitempty"`
	CreatedAt      int64               `json:"createdAt"`
	ExpiresAt      int64               `json:"expiresAt"`
}

// ListAgentProposalsInput is the request for GET /tasks/{id}/agent-proposals.
type ListAgentProposalsInput struct {
	ID     string `path:"id"`
	Status string `query:"status" enum:"pending,all" default:"pending" doc:"pending returns every live pending proposal; all returns the newest, up to limit"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"Applies to status=all"`
}

// ListAgentProposalsBody is the response payload.
type ListAgentProposalsBody struct {
	Total     int64           `json:"total"`
	Proposals []AgentProposal `json:"proposals"`
}

// ListAgentProposalsOutput is the response for GET /tasks/{id}/agent-proposals.
type ListAgentProposalsOutput struct {
	Body ListAgentProposalsBody
}

// DecideAgentProposalInput is the path for approve and reject.
type DecideAgentProposalInput struct {
	ID         string `path:"id"`
	ProposalID string `path:"proposalId" doc:"Proposal public id (UUID v7)"`
}

// RejectAgentProposalInput is the request for POST
// /tasks/{id}/agent-proposals/{proposalId}/reject.
type RejectAgentProposalInput struct {
	ID         string `path:"id"`
	ProposalID string `path:"proposalId" doc:"Proposal public id (UUID v7)"`
	Body       struct {
		Reason string `json:"reason,omitempty" maxLength:"500" doc:"Why the proposal is rejected; the agent reads it on its next run"`
	}
}

// AgentProposalDecision is the result of a verdict.
type AgentProposalDecision struct {
	ID     string `json:"id"`
	Status string `json:"status" enum:"applied,rejected"`
}

// DecideAgentProposalOutput is the response for approve and reject.
type DecideAgentProposalOutput struct {
	Body AgentProposalDecision
}

// agentTaskError turns what taskservice returns into the problem response.
func agentTaskError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, execution.ErrTaskOutsideWorkspace):
		return httpErr(apierrors.WsTaskNotFound)
	case errors.Is(err, taskservice.ErrDecision), errors.Is(err, taskservice.ErrProposalFilter):
		return httpErr(apierrors.ValidationBodyFieldInvalid)
	}
	var apiErr *apierr.APIError
	if errors.As(err, &apiErr) {
		return taskMutationHTTPError(err)
	}
	nflog.LoggerFromContext(ctx).ErrorContext(ctx, "agent proposal request failed", slog.Any("err", err))
	return httpErr(apierrors.InternalUnexpected)
}

// agentTaskCaller resolves the signed-in person as the principal taskservice
// authorizes.
func agentTaskCaller(ctx context.Context, deps Deps) (execution.Principal, types.PublicID, error) {
	ws, ok := middleware.WorkspaceFromContext(ctx)
	if !ok {
		return execution.Principal{}, types.PublicID{}, httpErr(apierrors.WsTaskNotFound)
	}
	task, ok := middleware.TaskFromContext(ctx)
	if !ok {
		return execution.Principal{}, types.PublicID{}, httpErr(apierrors.WsTaskNotFound)
	}
	actorID, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return execution.Principal{}, types.PublicID{}, httpErr(apierrors.WsTaskAccessDenied)
	}
	if deps.TaskService == nil {
		return execution.Principal{}, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
	}
	principal, err := execution.NewUserPrincipal(ws.ID, actorID)
	if err != nil {
		return execution.Principal{}, types.PublicID{}, httpErr(apierrors.InternalUnexpected)
	}
	return principal, types.FromUUID(task.PublicID), nil
}

// ListAgentProposals handles GET /tasks/{id}/agent-proposals.
func ListAgentProposals(deps Deps) func(context.Context, *ListAgentProposalsInput) (*ListAgentProposalsOutput, error) {
	return func(ctx context.Context, in *ListAgentProposalsInput) (*ListAgentProposalsOutput, error) {
		principal, task, err := agentTaskCaller(ctx, deps)
		if err != nil {
			return nil, err
		}
		proposals, err := deps.TaskService.ListProposals(ctx, principal, task, taskservice.ProposalFilter{
			Status: in.Status,
			Limit:  int(in.Limit),
		})
		if err != nil {
			return nil, agentTaskError(ctx, err)
		}
		out := &ListAgentProposalsOutput{}
		out.Body.Proposals = make([]AgentProposal, 0, len(proposals))
		for _, p := range proposals {
			out.Body.Proposals = append(out.Body.Proposals, proposalToDTO(p))
		}
		out.Body.Total = int64(len(out.Body.Proposals))
		return out, nil
	}
}

// ApproveAgentProposal handles POST /tasks/{id}/agent-proposals/{proposalId}/approve.
func ApproveAgentProposal(deps Deps) func(context.Context, *DecideAgentProposalInput) (*DecideAgentProposalOutput, error) {
	return func(ctx context.Context, in *DecideAgentProposalInput) (*DecideAgentProposalOutput, error) {
		return decideAgentProposal(ctx, deps, in.ProposalID, taskservice.Decision{Verdict: taskservice.VerdictApprove})
	}
}

// RejectAgentProposal handles POST /tasks/{id}/agent-proposals/{proposalId}/reject.
func RejectAgentProposal(deps Deps) func(context.Context, *RejectAgentProposalInput) (*DecideAgentProposalOutput, error) {
	return func(ctx context.Context, in *RejectAgentProposalInput) (*DecideAgentProposalOutput, error) {
		return decideAgentProposal(ctx, deps, in.ProposalID, taskservice.Decision{Verdict: taskservice.VerdictReject, Reason: in.Body.Reason})
	}
}

func decideAgentProposal(ctx context.Context, deps Deps, proposalID string, d taskservice.Decision) (*DecideAgentProposalOutput, error) {
	principal, task, err := agentTaskCaller(ctx, deps)
	if err != nil {
		return nil, err
	}
	proposal, err := types.Parse(proposalID)
	if err != nil {
		return nil, httpErr(apierrors.WsAgentProposalNotFound)
	}
	res, err := deps.TaskService.Decide(ctx, principal, task, proposal, d)
	if err != nil {
		return nil, agentTaskError(ctx, err)
	}
	return &DecideAgentProposalOutput{Body: AgentProposalDecision{ID: proposal.String(), Status: string(res.Status)}}, nil
}

func proposalToDTO(p taskservice.Proposal) AgentProposal {
	dto := AgentProposal{
		ID:    p.ID.String(),
		Agent: AgentRef{ID: p.AgentID.String(), Name: p.AgentName},
		Kind:  p.Kind,
		Args: AgentProposalArgs{
			Body:       p.Args.Body,
			Priority:   p.Args.Priority,
			DueOn:      p.Args.DueOn,
			Transition: p.Args.Transition,
			Title:      p.Args.Title,
		},
		Before: AgentProposalBefore{
			Priority:     p.Before.Priority,
			DueOn:        p.Before.DueOn,
			DerivedState: p.Before.DerivedState,
		},
		Status:         p.Status,
		ErrorCode:      p.ErrorCode,
		DecisionReason: p.DecisionReason,
		CreatedAt:      p.CreatedAt.Unix(),
		ExpiresAt:      p.ExpiresAt.Unix(),
	}
	if p.DecidedBy != nil {
		dto.DecidedBy = &AgentProposalUser{ID: p.DecidedBy.ID.String(), Name: p.DecidedBy.DisplayName}
	}
	if p.DecidedAt != nil {
		at := p.DecidedAt.Unix()
		dto.DecidedAt = &at
	}
	return dto
}
