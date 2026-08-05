package ai

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/stateinfer"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// stateSuggestionsLimit caps how many tasks we evaluate per call. The
// rule engine is cheap (no LLM) but each row still pays a goroutine /
// allocation cost; 200 keeps p99 latency bounded.
const stateSuggestionsLimit = 200

// ListStateSuggestionsInput is the path input for
// GET /workspaces/{wsId}/ai/state-suggestions.
type ListStateSuggestionsInput struct {
	WsID string `path:"wsId"`
}

// StateSuggestion is a single (task, proposal) pair returned by the
// workspace-wide stateinfer batch endpoint. Tasks with no proposal are
// omitted from the response so callers see only actionable rows.
type StateSuggestion struct {
	TaskID     string  `json:"taskId"`
	Title      string  `json:"title"`
	State      string  `json:"state"`
	Transition string  `json:"transition"`
	Confidence float32 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ListStateSuggestionsOutput wraps the suggestion list. The total field
// reflects how many tasks were *evaluated*, not how many produced
// suggestions, so the client can show "scanned N tasks → M suggestions".
type ListStateSuggestionsOutput struct {
	Body struct {
		Total       int               `json:"total"`
		Suggestions []StateSuggestion `json:"suggestions"`
	}
}

// ListStateSuggestions handles GET /workspaces/{wsId}/ai/state-suggestions.
// It walks the workspace's task list view (capped at 200), runs the
// deterministic stateinfer rules over each row, and returns the rows
// that produced a confident proposal. No LLM call is made.
func ListStateSuggestions(deps Deps) func(context.Context, *ListStateSuggestionsInput) (*ListStateSuggestionsOutput, error) {
	return func(ctx context.Context, _ *ListStateSuggestionsInput) (*ListStateSuggestionsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		// These endpoints read the workspace task list to derive a
		// suggestion, and every suggestion carries the task's title. So
		// the read takes the same visibility filter a plain task list
		// does: an AI feature is a reader like any other, and deriving
		// a recommendation from a task does not grant sight of it.

		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID:   ws.ID,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			Limit:         stateSuggestionsLimit,
			Offset:        0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now().UTC()
		out := &ListStateSuggestionsOutput{}
		out.Body.Total = len(rows)
		out.Body.Suggestions = []StateSuggestion{}
		for _, r := range rows {
			updated := r.CreatedAt
			if r.UpdatedAt.Valid {
				updated = r.UpdatedAt.Time
			}
			sig := stateinfer.Signals{
				State:     stateinfer.State(r.DerivedState),
				UpdatedAt: updated,
				Now:       now,
			}
			if r.DueOn.Valid {
				sig.HasDueOn = true
				sig.DueOn = r.DueOn.Time
			}
			prop := stateinfer.Infer(sig)
			if prop == nil {
				continue
			}
			out.Body.Suggestions = append(out.Body.Suggestions, StateSuggestion{
				TaskID:     r.PublicID.String(),
				Title:      r.Title,
				State:      string(r.DerivedState),
				Transition: string(prop.Transition),
				Confidence: prop.Confidence,
				Reason:     prop.Reason,
			})
		}
		return out, nil
	}
}
