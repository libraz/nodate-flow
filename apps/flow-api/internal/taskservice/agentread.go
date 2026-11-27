package taskservice

import (
	"context"
	"slices"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
)

// Bounds on what an agent's prompt carries about its task.
const (
	MaxContextDescriptionRunes = 8000
	MaxContextComments         = 20
	MaxContextCommentRunes     = 1000
	MaxContextChildren         = 20
	MaxContextRejected         = 5
)

// AgentTask is the task as an agent reads it. DueOn / StartedOn are
// YYYY-MM-DD or empty.
type AgentTask struct {
	ID                   types.PublicID
	Title                string
	Description          string
	DescriptionTruncated bool
	State                string
	Priority             int32
	DueOn                string
	StartedOn            string
}

// AgentComment is one comment in an agent's context.
type AgentComment struct {
	AuthorKind string
	AuthorName string
	Body       string
	Truncated  bool
	CreatedAt  time.Time
}

// AgentChild is one public child task.
type AgentChild struct {
	ID    types.PublicID
	Title string
	State string
}

// AgentPendingProposal is one of the agent's own live proposals.
type AgentPendingProposal struct {
	ID        types.PublicID
	Kind      string
	Args      ProposalArgs
	CreatedAt time.Time
	ExpiresAt time.Time
}

// AgentRejectedProposal is a recent rejection with the human's reason.
type AgentRejectedProposal struct {
	ID        types.PublicID
	Kind      string
	Args      ProposalArgs
	Reason    string
	DecidedAt *time.Time
}

// AgentTaskContext is everything one run reads, from one transaction.
type AgentTaskContext struct {
	Task AgentTask
	// ProjectID is the internal project id an Action of kind create_subtask carries.
	ProjectID         uint32
	Comments          []AgentComment
	Children          []AgentChild
	PendingProposals  []AgentPendingProposal
	RejectedProposals []AgentRejectedProposal
	// ContextEventID is the highest task event id the context reflects.
	ContextEventID int64
	Grants         execution.ToolGrants
}

// ReadForAgent authorizes the agent and reads its task context in one
// transaction. Authorization refusals are returned as they are.
func (s *Service) ReadForAgent(ctx context.Context, agent execution.Principal, task types.PublicID) (AgentTaskContext, error) {
	if err := s.checkReadiness(false); err != nil {
		return AgentTaskContext{}, err
	}
	if !agent.IsAgent() {
		return AgentTaskContext{}, execution.ErrInvalidPrincipal
	}
	var out AgentTaskContext
	err := dbretry.InTx(ctx, s.DB, "taskservice.ReadForAgent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		c, err := s.readForAgentTx(ctx, tx, agent, task)
		out = c
		return err
	})
	if err != nil {
		return AgentTaskContext{}, err
	}
	return out, nil
}

func (s *Service) readForAgentTx(ctx context.Context, tx *dbretry.Tx, agent execution.Principal, task types.PublicID) (AgentTaskContext, error) {
	ws := agent.WorkspaceID()
	authz, err := execution.AuthorizeTaskTx(ctx, tx, agent, task, execution.CapabilityReadTask)
	if err != nil {
		return AgentTaskContext{}, err
	}
	qtx := s.Queries.WithTx(tx.RawTx())
	row, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{WorkspaceID: ws, PublicID: task})
	if err != nil {
		return AgentTaskContext{}, err
	}
	desc := truncateRunes(row.Description.String, MaxContextDescriptionRunes)
	out := AgentTaskContext{
		Task: AgentTask{
			ID:                   row.PublicID,
			Title:                row.Title,
			Description:          desc,
			DescriptionTruncated: desc != row.Description.String,
			State:                string(row.DerivedState),
			Priority:             row.Priority,
			DueOn:                dateOnly(row.DueOn),
			StartedOn:            dateOnly(row.StartedOn),
		},
		ProjectID: authz.Task.ProjectID,
	}
	taskRef := nullInt32(authz.Task.ID)

	agentRow, err := qtx.GetAgentKindByPublicID(ctx, generated.GetAgentKindByPublicIDParams{WorkspaceID: ws, PublicID: authz.Agent.PublicID})
	if err != nil {
		return AgentTaskContext{}, err
	}
	if out.Grants, err = execution.ParseToolGrants(agentRow.ToolsJson); err != nil {
		return AgentTaskContext{}, apierrors.Wrap(apierrors.InternalUnexpected, err)
	}

	comments, err := qtx.ListAgentContextComments(ctx, generated.ListAgentContextCommentsParams{WorkspaceID: ws, TaskID: taskRef, Limit: MaxContextComments})
	if err != nil {
		return AgentTaskContext{}, err
	}
	slices.Reverse(comments)
	for _, c := range comments {
		name := c.AuthorDisplayName.String
		if c.AuthorKind == generated.CommentsAuthorKindAgent {
			name = c.AuthorAgentName.String
		}
		body := truncateRunes(c.Body, MaxContextCommentRunes)
		out.Comments = append(out.Comments, AgentComment{
			AuthorKind: string(c.AuthorKind),
			AuthorName: name,
			Body:       body,
			Truncated:  body != c.Body,
			CreatedAt:  c.CreatedAt,
		})
	}

	children, err := qtx.ListAgentContextChildTasks(ctx, generated.ListAgentContextChildTasksParams{WorkspaceID: ws, ParentTaskID: taskRef, Limit: MaxContextChildren})
	if err != nil {
		return AgentTaskContext{}, err
	}
	for _, c := range children {
		out.Children = append(out.Children, AgentChild{ID: c.PublicID, Title: c.Title, State: string(c.DerivedState)})
	}

	pending, err := qtx.ListPendingProposalsForTaskAgent(ctx, generated.ListPendingProposalsForTaskAgentParams{
		WorkspaceID: ws, TaskID: authz.Task.ID, AgentID: authz.Agent.ID, Now: time.Now().UTC(), Limit: MaxPendingProposals,
	})
	if err != nil {
		return AgentTaskContext{}, err
	}
	for _, p := range pending {
		_, args, err := decodeProposalArgs(p.Kind, p.ArgsJson)
		if err != nil {
			return AgentTaskContext{}, err
		}
		out.PendingProposals = append(out.PendingProposals, AgentPendingProposal{
			ID: p.PublicID, Kind: string(p.Kind), Args: args, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
		})
	}

	rejected, err := qtx.ListRecentRejectedProposals(ctx, generated.ListRecentRejectedProposalsParams{
		WorkspaceID: ws, TaskID: authz.Task.ID, AgentID: authz.Agent.ID, Limit: MaxContextRejected,
	})
	if err != nil {
		return AgentTaskContext{}, err
	}
	for _, p := range rejected {
		_, args, err := decodeProposalArgs(p.Kind, p.ArgsJson)
		if err != nil {
			return AgentTaskContext{}, err
		}
		r := AgentRejectedProposal{ID: p.PublicID, Kind: string(p.Kind), Args: args, Reason: p.DecisionReason.String}
		if p.DecidedAt.Valid {
			at := p.DecidedAt.Time
			r.DecidedAt = &at
		}
		out.RejectedProposals = append(out.RejectedProposals, r)
	}

	if out.ContextEventID, err = qtx.GetMaxTaskEventID(ctx, generated.GetMaxTaskEventIDParams{WorkspaceID: ws, TaskID: taskRef}); err != nil {
		return AgentTaskContext{}, err
	}
	return out, nil
}
