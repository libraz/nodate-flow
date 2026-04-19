package tasks

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/constraint/engine"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// EvaluateConstraintsInput is the path input for
// POST /tasks/{id}/constraints/evaluate.
type EvaluateConstraintsInput struct {
	ID string `path:"id"`
}

// EvaluateConstraintsOutcome mirrors engine.Outcome on the wire.
type EvaluateConstraintsOutcome struct {
	ID         string `json:"id"`
	Satisfied  bool   `json:"satisfied"`
	ParseError string `json:"parseError,omitempty"`
}

// EvaluateConstraintsOutput wraps the evaluated outcomes.
type EvaluateConstraintsOutput struct {
	Body struct {
		Outcomes []EvaluateConstraintsOutcome `json:"outcomes"`
	}
}

// EvaluateConstraints handles POST /tasks/{id}/constraints/evaluate.
// It runs the constraint engine against the current task facts and
// persists satisfied_at / failed_at markers as a side-effect. The
// response is a per-constraint outcome list for the State Graph UI.
func EvaluateConstraints(deps Deps) func(context.Context, *EvaluateConstraintsInput) (*EvaluateConstraintsOutput, error) {
	return func(ctx context.Context, _ *EvaluateConstraintsInput) (*EvaluateConstraintsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		eng := &engine.Engine{
			Store: &engine.SqlcStore{WorkspaceID: ws.ID, Queries: deps.Queries},
		}
		outs, err := eng.EvaluateTask(ctx, task.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &EvaluateConstraintsOutput{}
		out.Body.Outcomes = make([]EvaluateConstraintsOutcome, 0, len(outs))
		for _, o := range outs {
			item := EvaluateConstraintsOutcome{ID: o.PublicID, Satisfied: o.Satisfied}
			if o.ParseError != nil {
				item.ParseError = o.ParseError.Error()
			}
			out.Body.Outcomes = append(out.Body.Outcomes, item)
		}
		return out, nil
	}
}
