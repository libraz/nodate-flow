package tasks

import (
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

func TestProposalToDTO_PublicShape(t *testing.T) {
	prio := int32(3)
	due := "2026-12-01"
	created := time.Unix(1_700_000_000, 0)
	decidedAt := time.Unix(1_700_000_500, 0)
	id, agent, user := types.New(), types.New(), types.New()

	dto := proposalToDTO(taskservice.Proposal{
		ID:             id,
		AgentID:        agent,
		AgentName:      "triager",
		Kind:           "set_priority",
		Args:           taskservice.ProposalArgs{Priority: &prio},
		Before:         taskservice.ProposalBefore{Priority: &prio, DueOn: &due, DerivedState: "open"},
		Status:         "rejected",
		DecisionReason: "not now",
		DecidedBy:      &taskservice.ProposalUser{ID: user, DisplayName: "Sam"},
		DecidedAt:      &decidedAt,
		CreatedAt:      created,
		ExpiresAt:      created.Add(7 * 24 * time.Hour),
	})

	if dto.ID != id.String() || dto.Agent.ID != agent.String() || dto.Agent.Name != "triager" {
		t.Fatalf("ids not mapped to public ids: %+v", dto)
	}
	if dto.CreatedAt != 1_700_000_000 || dto.ExpiresAt != 1_700_000_000+7*24*3600 {
		t.Fatalf("times must be unix seconds, got %d / %d", dto.CreatedAt, dto.ExpiresAt)
	}
	if dto.DecidedAt == nil || *dto.DecidedAt != 1_700_000_500 {
		t.Fatalf("decidedAt = %v", dto.DecidedAt)
	}
	if dto.DecidedBy == nil || dto.DecidedBy.ID != user.String() || dto.DecidedBy.Name != "Sam" {
		t.Fatalf("decidedBy = %+v", dto.DecidedBy)
	}
	if dto.Args.Priority == nil || *dto.Args.Priority != 3 || dto.Before.DerivedState != "open" {
		t.Fatalf("args/before not carried: %+v", dto)
	}
}

func TestProposalToDTO_Pending(t *testing.T) {
	dto := proposalToDTO(taskservice.Proposal{ID: types.New(), AgentID: types.New(), Kind: "add_comment", Status: "pending"})
	if dto.DecidedBy != nil || dto.DecidedAt != nil {
		t.Fatalf("a pending proposal has no decision: %+v", dto)
	}
}
