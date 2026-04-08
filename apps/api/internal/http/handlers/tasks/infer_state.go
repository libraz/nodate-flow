package tasks

import (
	"context"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/stateinfer"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// InferStateInput is the Huma input for GET /tasks/{id}/infer-state.
type InferStateInput struct {
	ID string `path:"id"`
}

// InferStateProposal mirrors stateinfer.Proposal at the API boundary.
type InferStateProposal struct {
	Transition string  `json:"transition"`
	Confidence float32 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// InferStateOutput wraps the suggestion. Proposal is nil when the
// deterministic rules produced no confident suggestion; the TaskID is
// always echoed so the client can correlate.
type InferStateOutput struct {
	Body struct {
		TaskID   string              `json:"taskId"`
		State    string              `json:"state"`
		Proposal *InferStateProposal `json:"proposal,omitempty"`
	}
}

// InferState handles GET /tasks/{id}/infer-state. It reads the current
// task detail view, folds it into stateinfer.Signals, and returns the
// (possibly nil) proposal from the rule-based inference engine
// (2.AI-1).
func InferState(deps Deps) func(context.Context, *InferStateInput) (*InferStateOutput, error) {
	return func(ctx context.Context, _ *InferStateInput) (*InferStateOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		updated := row.CreatedAt
		if row.UpdatedAt.Valid {
			updated = row.UpdatedAt.Time
		}
		sig := stateinfer.Signals{
			State:           stateinfer.State(row.DerivedState),
			UpdatedAt:       updated,
			DependencyCount: row.DependencyCount,
			Now:             time.Now().UTC(),
		}
		if row.DueOn.Valid {
			sig.HasDueOn = true
			sig.DueOn = row.DueOn.Time
		}
		prop := stateinfer.Infer(sig)

		out := &InferStateOutput{}
		out.Body.TaskID = task.PublicID.String()
		out.Body.State = string(row.DerivedState)
		if prop != nil {
			out.Body.Proposal = &InferStateProposal{
				Transition: string(prop.Transition),
				Confidence: prop.Confidence,
				Reason:     prop.Reason,
			}
		}
		return out, nil
	}
}
