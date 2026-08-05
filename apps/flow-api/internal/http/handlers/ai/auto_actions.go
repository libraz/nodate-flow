package ai

import (
	"context"
	"sort"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// autoActionsLimit caps how many tasks we evaluate per call. The rule
// engine is pure Go and cheap, but we still bound the list-view query.
const autoActionsLimit = 200

// ListAutoActionsInput is the path input for
// GET /workspaces/{wsId}/ai/auto-actions.
type ListAutoActionsInput struct {
	WsID string `path:"wsId"`
}

// TaskAutoAction is a single (task, action) pair surfaced to the UI.
type TaskAutoAction struct {
	TaskID     string  `json:"taskId"`
	Title      string  `json:"title"`
	State      string  `json:"state"`
	Kind       string  `json:"kind"`
	Confidence float32 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ListAutoActionsOutput wraps the action list. Total is the number of
// tasks evaluated (capped at autoActionsLimit), not the number of
// actions emitted.
type ListAutoActionsOutput struct {
	Body struct {
		Total   int              `json:"total"`
		Actions []TaskAutoAction `json:"actions"`
	}
}

// actionWeight orders actions from most to least urgent for the
// response list. Escalate first, then assign-owner, then nudge, then
// close-stale-review.
var actionWeight = map[autoactions.Kind]int{
	autoactions.KindEscalateOverdue:  0,
	autoactions.KindAssignOwner:      1,
	autoactions.KindNudgeAssignee:    2,
	autoactions.KindCloseStaleReview: 3,
}

// ListAutoActions handles GET /workspaces/{wsId}/ai/auto-actions. It
// walks the workspace's task list view, runs the deterministic
// auto-action rules on each row, and returns only rows that produced
// an action, sorted by urgency then by confidence. No LLM call is
// made.
func ListAutoActions(deps Deps) func(context.Context, *ListAutoActionsInput) (*ListAutoActionsOutput, error) {
	return func(ctx context.Context, _ *ListAutoActionsInput) (*ListAutoActionsOutput, error) {
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
			Limit:         autoActionsLimit,
			Offset:        0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now().UTC()
		out := &ListAutoActionsOutput{}
		out.Body.Total = len(rows)
		out.Body.Actions = []TaskAutoAction{}
		for _, r := range rows {
			sig := autoactions.Signals{
				State:         autoactions.State(r.DerivedState),
				AssigneeCount: r.AssigneeCount,
				// Read from the count, not from a type assertion on the
				// primary assignee's public id. That column is an
				// aggregate the view builds with MIN(...), so sqlc types
				// it interface{} and the driver is free to hand it back
				// as something other than []byte — in which case the
				// assertion silently yields false and every task looks
				// unassigned. The rules that key off this then propose
				// an owner for tasks that already have one, which is
				// what a user sees. AssigneeCount is the same fact,
				// typed, in the same row.
				HasAssignee: r.AssigneeCount > 0,
				Now:         now,
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
			act := autoactions.Evaluate(sig)
			if act == nil {
				continue
			}
			out.Body.Actions = append(out.Body.Actions, TaskAutoAction{
				TaskID:     r.PublicID.String(),
				Title:      r.Title,
				State:      string(r.DerivedState),
				Kind:       string(act.Kind),
				Confidence: act.Confidence,
				Reason:     act.Reason,
			})
		}
		sort.SliceStable(out.Body.Actions, func(i, j int) bool {
			wi := actionWeight[autoactions.Kind(out.Body.Actions[i].Kind)]
			wj := actionWeight[autoactions.Kind(out.Body.Actions[j].Kind)]
			if wi != wj {
				return wi < wj
			}
			return out.Body.Actions[i].Confidence > out.Body.Actions[j].Confidence
		})
		return out, nil
	}
}
