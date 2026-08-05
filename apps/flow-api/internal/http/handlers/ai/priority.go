// Package ai — priority.go exposes the priority-optimization stream
// It walks the workspace's open tasks, runs the
// deterministic priorityopt rules on each row, and returns only the
// rows whose suggested priority differs from the current priority.
package ai

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/priorityopt"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// priorityOptLimit caps how many tasks we evaluate per call.
const priorityOptLimit = 200

// ListPrioritySuggestionsInput is the path input for
// GET /workspaces/{wsId}/ai/priority-suggestions.
type ListPrioritySuggestionsInput struct {
	WsID string `path:"wsId"`
}

// TaskPrioritySuggestion is a single (task, suggested priority) pair
// surfaced to the UI.
type TaskPrioritySuggestion struct {
	TaskID            string  `json:"taskId"`
	Title             string  `json:"title"`
	CurrentPriority   int32   `json:"currentPriority"`
	SuggestedPriority int32   `json:"suggestedPriority"`
	Confidence        float32 `json:"confidence"`
	Reason            string  `json:"reason"`
}

// ListPrioritySuggestionsOutput wraps the suggestion list. Total is
// the number of tasks evaluated, not the number of suggestions emitted.
type ListPrioritySuggestionsOutput struct {
	Body struct {
		Total       int                      `json:"total"`
		Suggestions []TaskPrioritySuggestion `json:"suggestions"`
	}
}

// ListPrioritySuggestions handles
// GET /workspaces/{wsId}/ai/priority-suggestions. Done and cancelled
// tasks are filtered out before reaching the rule engine because
// their priority is irrelevant.
func ListPrioritySuggestions(deps Deps) func(context.Context, *ListPrioritySuggestionsInput) (*ListPrioritySuggestionsOutput, error) {
	return func(ctx context.Context, _ *ListPrioritySuggestionsInput) (*ListPrioritySuggestionsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       priorityOptLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now().UTC()
		signals := make([]priorityopt.Signals, 0, len(rows))
		for _, r := range rows {
			state := priorityopt.State(r.DerivedState)
			if state != priorityopt.StateOpen && state != priorityopt.StateWaiting && state != priorityopt.StateReview {
				continue
			}
			sig := priorityopt.Signals{
				TaskID:          r.PublicID.String(),
				Title:           r.Title,
				State:           state,
				CurrentPriority: r.Priority,
				HasAssignee:     func() bool { b, ok := r.PrimaryAssigneePublicID.([]byte); return ok && len(b) > 0 }(),
				Now:             now,
			}
			if r.UpdatedAt.Valid {
				sig.UpdatedAt = r.UpdatedAt.Time
			} else {
				sig.UpdatedAt = r.CreatedAt
			}
			if r.DueOn.Valid {
				sig.HasDueOn = true
				sig.DueOn = r.DueOn.Time
			}
			signals = append(signals, sig)
		}

		results := priorityopt.Evaluate(signals)
		out := &ListPrioritySuggestionsOutput{}
		out.Body.Total = len(signals)
		out.Body.Suggestions = make([]TaskPrioritySuggestion, 0, len(results))
		for _, s := range results {
			out.Body.Suggestions = append(out.Body.Suggestions, TaskPrioritySuggestion{
				TaskID:            s.TaskID,
				Title:             s.Title,
				CurrentPriority:   s.CurrentPriority,
				SuggestedPriority: s.SuggestedPriority,
				Confidence:        s.Confidence,
				Reason:            s.Reason,
			})
		}
		return out, nil
	}
}
