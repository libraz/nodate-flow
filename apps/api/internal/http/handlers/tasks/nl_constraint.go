package tasks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlconstraint"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/constraint"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// CompileConstraint handles POST /tasks/{id}/constraints/compile.
// It takes a natural-language prompt, runs it through the NL-to-DSL
// compiler, and returns the inferred constraint kind and DSL expression.
func CompileConstraint(deps Deps) func(context.Context, *CompileConstraintInput) (*CompileConstraintOutput, error) {
	return func(ctx context.Context, in *CompileConstraintInput) (*CompileConstraintOutput, error) {
		if deps.NlConstraint == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}
		parsed, err := deps.NlConstraint.Compile(ctx, in.Body.Prompt)
		if err != nil {
			if errors.Is(err, nlconstraint.ErrUnparseable) {
				return nil, httpErr(apierrors.AiResponseParseFailed)
			}
			return nil, httpErr(apierrors.AiProviderUpstreamCallFailed)
		}
		raw, err := json.Marshal(parsed)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &CompileConstraintOutput{}
		out.Body.Kind = string(parsed.Op)
		out.Body.Expression = string(raw)
		return out, nil
	}
}

// ExplainConstraint handles POST /tasks/{id}/constraints/explain.
// It parses a DSL expression and returns a human-readable explanation.
func ExplainConstraint(_ Deps) func(context.Context, *ExplainConstraintInput) (*ExplainConstraintOutput, error) {
	return func(_ context.Context, in *ExplainConstraintInput) (*ExplainConstraintOutput, error) {
		parsed, err := constraint.Parse([]byte(in.Body.Expression))
		if err != nil {
			return nil, httpErr(apierrors.ConstraintParseInvalidJson)
		}
		out := &ExplainConstraintOutput{}
		out.Body.Explanation = constraint.Explain(parsed)
		return out, nil
	}
}
