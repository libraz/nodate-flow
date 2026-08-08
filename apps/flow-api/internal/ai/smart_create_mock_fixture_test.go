package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestMockFixtureRoutingForSmartCreate proves the mock provider answers the
// smart-create system prompt with the smart-create fixture.
//
// The mock routes on a substring of the system prompt because the providers
// package cannot import this one, so nothing in the compiler keeps the two
// in agreement. This test is that link: it feeds the real prompt constant
// through the real provider and fails if the answer is the inbox-triage
// fallback, which would otherwise surface only as a confidently wrong
// proposal much further downstream.
func TestMockFixtureRoutingForSmartCreate(t *testing.T) {
	t.Parallel()

	prov := providers.NewMockProvider("")
	resp, err := prov.Complete(context.Background(), providers.Request{
		System: smartCreateSystemPrompt,
		Prompt: "any prompt; the mock ignores it",
	})
	if err != nil {
		t.Fatalf("mock provider: %v", err)
	}

	var proposal SmartProposal
	if err := json.Unmarshal([]byte(resp.Text), &proposal); err != nil {
		t.Fatalf("smart-create prompt must resolve to the smart_create fixture, got %q: %v", resp.Text, err)
	}
	if len(proposal.Subtasks) < 2 {
		t.Fatalf("the smart_create fixture must propose at least two subtasks so callers that insert children "+
			"exercise more than a single row; got %d", len(proposal.Subtasks))
	}
	for i, sub := range proposal.Subtasks {
		if sub.Title == "" {
			t.Errorf("fixture subtask %d has an empty title, which callers skip", i)
		}
	}
}

// TestMockFixtureRoutingForInboxTriage proves the inbox-triage prompt still
// reaches its own fixture now that it is routed by marker rather than
// reached by falling through.
func TestMockFixtureRoutingForInboxTriage(t *testing.T) {
	t.Parallel()

	prov := providers.NewMockProvider("")
	resp, err := prov.Complete(context.Background(), providers.Request{
		System: proposeInboxTriageSystem,
	})
	if err != nil {
		t.Fatalf("mock provider: %v", err)
	}
	var triage []struct {
		InboxItemID string `json:"inboxItemId"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &triage); err != nil {
		t.Fatalf("inbox-triage prompt must resolve to the inbox_triage fixture, got %q: %v", resp.Text, err)
	}
	if len(triage) == 0 {
		t.Fatal("the inbox_triage fixture must stay non-empty")
	}
}

// TestMockFixtureUnroutedPurposeFails proves an unrouted purpose is refused
// rather than answered with somebody else's fixture. Answering it produced a
// proposal in the wrong shape that still looked deliberate, and the caller
// had no way to tell.
func TestMockFixtureUnroutedPurposeFails(t *testing.T) {
	t.Parallel()

	prov := providers.NewMockProvider("")
	resp, err := prov.Complete(context.Background(), providers.Request{
		System: "some other orchestrator purpose",
	})
	if !errors.Is(err, providers.ErrMockFixtureUnrouted) {
		t.Fatalf("unrouted purpose must fail with ErrMockFixtureUnrouted; got resp=%v err=%v", resp, err)
	}
}
