// Package ai - inbox.go is the entry point for AI-driven inbox
// triage. ProposeInboxTriage walks the top-N unarchived inbox items
// for a workspace, asks the wired Provider to score them, and appends a
// ai.suggestion.proposed event for each result so the Glass Dock and
// constraint engine can react.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// InboxTriageSuggestion is the per-item result returned by
// ProposeInboxTriage. InboxItemID is the public UUID of the inbox row;
// Score is in [0,1]; RecommendedAction is one of "archive", "snooze",
// "open".
type InboxTriageSuggestion struct {
	InboxItemID       string  `json:"inboxItemId"`
	Score             float32 `json:"score"`
	RecommendedAction string  `json:"recommendedAction"`
	Reasoning         string  `json:"reasoning"`
}

const proposeInboxTriageSystem = `You are an inbox triage assistant for nodate-flow. ` +
	`Reply ONLY with a JSON array of objects with keys "inboxItemId", "score" (0..1), ` +
	`"recommendedAction" ("archive" | "snooze" | "open"), "reasoning" (short string).`

// ProposeInboxTriage loads the top N unarchived inbox items for the
// workspace, asks the wired Provider to score them, appends one
// ai.suggestion.proposed event per returned suggestion, and returns the
// list. It enforces the cost guard before any LLM call.
func (o *Orchestrator) ProposeInboxTriage(ctx context.Context, workspaceID uint32, limit int) ([]InboxTriageSuggestion, error) {
	if o == nil || o.Resolver == nil {
		return nil, ErrNoProvider
	}
	if err := o.Guard.Check(ctx, workspaceID); err != nil {
		return nil, err
	}
	prov, err := o.Resolver.Default(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if prov == nil {
		return nil, ErrNoProvider
	}
	ctx = providers.WithWorkspaceID(ctx, workspaceID)

	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	// Build the user prompt from inbox items when Queries is wired.
	// When it is not (unit tests with a fake Provider), we still call
	// the provider with an empty list — the mock ignores the prompt.
	var prompt string
	if o.Queries != nil {
		rows, qerr := o.Queries.ListInbox(ctx, generated.ListInboxParams{
			WorkspaceID: workspaceID,
			Limit:       int32(limit),
			Offset:      0,
		})
		if qerr != nil {
			return nil, fmt.Errorf("ai: list inbox: %w", qerr)
		}
		prompt = buildInboxTriagePrompt(rows)
	}

	// ---- cache check (keyed on the prompt which includes item IDs) ----
	cacheKey := ProposalCacheKey(workspaceID, "inbox_triage", prompt)
	if cached, ok := o.ProposalCache.Get(cacheKey); ok {
		if s, _ := cached.([]InboxTriageSuggestion); s != nil {
			return s, nil
		}
	}

	req := airequest.New(prov, airequest.Args{
		System: proposeInboxTriageSystem,
		Prompt: prompt,
	})
	start := time.Now()
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		o.recordMetrics(string(prov.Kind()), req.Model, 0, 0, 0, time.Since(start), err)
		o.logFailure(ctx, workspaceID, "propose_inbox_triage", req, err)
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	o.recordMetrics(string(prov.Kind()), req.Model, resp.InputTokens, resp.OutputTokens, resp.EstimatedCostMicros(), time.Since(start), nil)
	o.logSuccess(ctx, workspaceID, "propose_inbox_triage", req, resp)

	suggestions, parseErr := parseInboxTriageSuggestions(resp.Text)
	if parseErr != nil {
		return nil, parseErr
	}

	// Append one ai.suggestion.proposed event per suggestion. A failure
	// on event N must not cost the caller the suggestions themselves —
	// they are the product of a paid, non-repeatable model call — so the
	// loss is recorded and the run continues.
	if o.DB != nil {
		for _, s := range suggestions {
			payload := map[string]any{
				"inbox_item_id": s.InboxItemID,
				"score":         s.Score,
				"action":        s.RecommendedAction,
				"reasoning":     s.Reasoning,
			}
			eventbus.AppendBestEffort(ctx, o.DB, eventbus.Event{
				Type:        eventbus.AiSuggestionProposed,
				WorkspaceID: workspaceID,
				Payload:     payload,
			}, "ai.propose_inbox_triage")
		}
	}
	o.ProposalCache.Put(cacheKey, suggestions)
	return suggestions, nil
}

// buildInboxTriagePrompt renders a compact, redacted summary of the
// inbox rows. Only the public id, source, kind and (truncated) task
// title travel into the prompt; raw payload_json is intentionally
// excluded so secrets in webhook bodies never reach the LLM.
func buildInboxTriagePrompt(rows []generated.ListInboxRow) string {
	if len(rows) == 0 {
		return "[]"
	}
	type item struct {
		InboxItemID string `json:"inboxItemId"`
		Source      string `json:"source"`
		Kind        string `json:"kind"`
		TaskTitle   string `json:"taskTitle,omitempty"`
	}
	out := make([]item, 0, len(rows))
	for _, r := range rows {
		entry := item{
			InboxItemID: r.PublicID.String(),
			Source:      string(r.Source),
			Kind:        r.Kind,
		}
		if r.TaskTitle.Valid {
			entry.TaskTitle = r.TaskTitle.String
		}
		out = append(out, entry)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// parseInboxTriageSuggestions tolerates the model wrapping the JSON
// array in prose or fenced code blocks. extractJSON pulls the first
// balanced "[...]" substring before json.Unmarshal runs.
func parseInboxTriageSuggestions(s string) ([]InboxTriageSuggestion, error) {
	payload := extractJSON(s)
	if payload == "" {
		return nil, ErrParse
	}
	var out []InboxTriageSuggestion
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	return out, nil
}
