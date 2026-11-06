// Package ai - inbox.go is the entry point for AI-driven inbox
// triage. ProposeInboxTriage walks the top-N unarchived inbox items
// for a workspace, asks the wired Provider to score them, and appends a
// ai.suggestion.proposed event for each result so the Glass Dock and
// constraint engine can react.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/inboxsnapshot"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
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

type inboxTriageCacheEntry struct {
	BatchID     types.PublicID
	Suggestions []InboxTriageSuggestion
}

const proposeInboxTriageSystem = `You are an inbox triage assistant for nodate-flow. ` +
	`Reply ONLY with a JSON array of objects with keys "inboxItemId", "score" (0..1), ` +
	`"recommendedAction" ("archive" | "snooze" | "open"), "reasoning" (short string). ` +
	`Each inbox item includes an "allowedActions" list; choose its recommendedAction from that list.`

// ProposeInboxTriage loads the top N unarchived inbox items for the
// workspace, asks the wired Provider to score them, appends one
// ai.suggestion.proposed event per returned suggestion, and returns the
// list. It enforces the cost guard before any LLM call.
//
// vis is the requesting actor's: a task title enters the prompt only
// when that actor may read the task, so neither the provider nor the
// cached proposal served back carries a title they could not open.
func (o *Orchestrator) ProposeInboxTriage(ctx context.Context, workspaceID uint32, vis acl.VisibilityArgs, limit int) ([]InboxTriageSuggestion, error) {
	if o == nil || o.Resolver == nil {
		return nil, ErrNoProvider
	}
	if o.DB == nil || o.SuggestionPrivacy == nil || vis.ActorUserID <= 0 || vis.ActorUserID > int64(^uint32(0)) {
		return nil, fmt.Errorf("ai: inbox suggestion privacy is not configured for an authenticated actor")
	}
	actorUserID := uint32(vis.ActorUserID)
	if _, err := CheckBudget(ctx, o.Guard, workspaceID); err != nil {
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

	// Build the user prompt and capture immutable per-item source versions
	// before the provider call. The snapshot is never reconstructed from a
	// later database read after the model has responded.
	// When it is not (unit tests with a fake Provider), we still call
	// the provider with an empty list — the mock ignores the prompt.
	var prompt string
	sourceVersions := map[string]string{}
	var taskRefs []types.PublicID
	openableInboxItems := map[string]struct{}{}
	if o.Queries != nil {
		rows, qerr := o.Queries.ListInbox(ctx, generated.ListInboxParams{
			WorkspaceID: workspaceID,
			IsElevated:  vis.IsElevated,
			ActorUserID: vis.ActorUserID,
			Limit:       int32(limit),
			Offset:      0,
		})
		if qerr != nil {
			return nil, fmt.Errorf("ai: list inbox: %w", qerr)
		}
		var resources []InvocationResource
		var captureErr error
		prompt, sourceVersions, resources, taskRefs, openableInboxItems, captureErr = captureInboxTriageInput(rows)
		if captureErr != nil {
			return nil, fmt.Errorf("ai: capture inbox triage input: %w", captureErr)
		}
		ctx = WithInputResources(ctx, resources...)
	}

	// Cache entries carry actor-owned provenance. Include the actor even for
	// elevated readers, whose task visibility scope is otherwise shared.
	cacheScope := fmt.Sprintf("%s:actor:%d", VisibilityCacheScope(vis), vis.ActorUserID)
	cacheKey := ProposalCacheKey(workspaceID, "inbox_triage", cacheScope, prompt)
	if cached, ok := o.ProposalCache.Get(cacheKey); ok {
		entry, valid := cached.(inboxTriageCacheEntry)
		if valid && entry.BatchID != (types.PublicID{}) {
			err := o.SuggestionPrivacy.Authorize(ctx, generated.New(o.DB), workspaceID, actorUserID, entry.BatchID)
			if err == nil {
				return append([]InboxTriageSuggestion(nil), entry.Suggestions...), nil
			}
			if !errors.Is(err, suggestionprivacy.ErrNotReadable) {
				return nil, fmt.Errorf("ai: authorize cached inbox suggestion batch: %w", err)
			}
		}
		o.ProposalCache.Delete(cacheKey)
	}

	req := airequest.New(prov, airequest.Args{
		System: proposeInboxTriageSystem,
		Prompt: prompt,
		Output: inboxTriageOutput,
	})
	var suggestions []InboxTriageSuggestion
	_, err = o.CompleteAndRecord(ctx, workspaceID, "propose_inbox_triage", prov, req, func(resp *providers.Response) error {
		var validationErr error
		suggestions, validationErr = validateInboxTriageOutput(resp, sourceVersions, openableInboxItems)
		return validationErr
	})
	if err != nil {
		return nil, fmt.Errorf("ai: provider call failed: %w", err)
	}
	if len(suggestions) == 0 {
		return suggestions, nil
	}
	refs := make([]suggestionprivacy.Ref, 0, len(taskRefs))
	for _, taskID := range taskRefs {
		refs = append(refs, suggestionprivacy.Ref{Kind: suggestionprivacy.ResourceTask, PublicID: taskID})
	}
	batchID, err := o.SuggestionPrivacy.Create(ctx, workspaceID, actorUserID, refs)
	if err != nil {
		return nil, fmt.Errorf("ai: persist inbox suggestion input provenance: %w", err)
	}
	if err := o.SuggestionPrivacy.Authorize(ctx, generated.New(o.DB), workspaceID, actorUserID, batchID); err != nil {
		return nil, fmt.Errorf("ai: inbox suggestion input is no longer readable: %w", err)
	}

	// Append one ai.suggestion.proposed event per suggestion. A failure
	// on event N must not cost the caller the suggestions themselves —
	// they are the product of a paid, non-repeatable model call — so the
	// loss is recorded and the run continues.
	if o.DB != nil {
		actorID := int64(actorUserID)
		for _, s := range suggestions {
			payload := map[string]any{
				"inbox_item_id":       s.InboxItemID,
				"score":               s.Score,
				"action":              s.RecommendedAction,
				"reasoning":           s.Reasoning,
				"suggestion_batch_id": batchID.String(),
			}
			if sourceVersion, ok := sourceVersions[s.InboxItemID]; ok {
				payload["source_version"] = sourceVersion
			}
			eventbus.AppendBestEffort(ctx, o.DB, eventbus.Event{
				Type:        eventbus.AiSuggestionProposed,
				WorkspaceID: workspaceID,
				ActorUserID: &actorID,
				Payload:     payload,
			}, "ai.propose_inbox_triage")
		}
	}
	o.ProposalCache.Put(cacheKey, inboxTriageCacheEntry{
		BatchID:     batchID,
		Suggestions: append([]InboxTriageSuggestion(nil), suggestions...),
	})
	return suggestions, nil
}

// CaptureInboxTriageSourceVersions returns the same server-side evidence
// digests attached to model-backed proposals. Deterministic fallback uses it
// on the exact inbox rows it evaluates so its result can use the same command
// contract without claiming a model saw that evidence.
func CaptureInboxTriageSourceVersions(rows []generated.ListInboxRow) (map[string]string, error) {
	_, versions, _, _, _, err := captureInboxTriageInput(rows)
	return versions, err
}

// CaptureInboxTriageTaskRefs returns every current, readable linked task ID
// captured with this actor's inbox rows, including tasks whose title is empty.
func CaptureInboxTriageTaskRefs(rows []generated.ListInboxRow) ([]types.PublicID, error) {
	_, _, _, refs, _, err := captureInboxTriageInput(rows)
	return refs, err
}

// captureInboxTriageInput renders the triage prompt from each row's bounded
// snapshot (public id, source, kind, projected source title and body excerpt,
// readable task title) plus the actions the model may propose for it. Raw
// payload_json and unreadable task titles never reach the provider.
func captureInboxTriageInput(rows []generated.ListInboxRow) (string, map[string]string, []InvocationResource, []types.PublicID, map[string]struct{}, error) {
	type promptItemWithActions struct {
		inboxsnapshot.PromptItem
		AllowedActions []string `json:"allowedActions"`
	}
	out := make([]promptItemWithActions, 0, len(rows))
	versions := make(map[string]string, len(rows))
	resources := make([]InvocationResource, 0, len(rows))
	taskRefs := make([]types.PublicID, 0, len(rows))
	openableInboxItems := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		fields := inboxsnapshot.Fields{
			InboxItemID: r.PublicID.String(),
			Source:      string(r.Source),
			Kind:        r.Kind,
			Payload:     r.PayloadJson,
			ReceivedAt:  r.ReceivedAt,
		}
		if r.UpdatedAt.Valid {
			fields.UpdatedAt = r.UpdatedAt.Time
		}
		if r.TaskVisible.Valid && r.TaskVisible.Bool && r.TaskPublicID.Valid {
			taskUUID, err := uuid.FromBytes([]byte(r.TaskPublicID.String))
			if err != nil || taskUUID == uuid.Nil {
				return "", nil, nil, nil, nil, fmt.Errorf("readable inbox task reference is not a valid BINARY(16) UUID")
			}
			taskID := types.FromUUID(taskUUID)
			taskRefs = append(taskRefs, taskID)
			openableInboxItems[r.PublicID.String()] = struct{}{}
			fields.TaskPublicID = taskID.String()
			fields.TaskReadable = true
			if r.TaskTitle.Valid {
				fields.TaskTitle = r.TaskTitle.String
			}
		}
		snapshot := inboxsnapshot.Capture(fields)
		allowedActions := []string{"archive", "snooze"}
		if fields.TaskReadable {
			allowedActions = append(allowedActions, "open")
		}
		out = append(out, promptItemWithActions{
			PromptItem:     snapshot.PromptItem(),
			AllowedActions: allowedActions,
		})
		versions[snapshot.InboxItemID] = snapshot.VersionHash()
		if snapshot.TaskTitle != "" && snapshot.TaskPublicID != "" {
			resources = append(resources, TaskInputResource(snapshot.TaskPublicID))
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]", versions, resources, taskRefs, openableInboxItems, err
	}
	return string(b), versions, resources, taskRefs, openableInboxItems, nil
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
