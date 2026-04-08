// Package nlconstraint compiles natural-language prose into a
// validated constraint DSL expression (Phase 3, 3.AI-1). It mirrors
// the shape of [nlquery]: a single-round LLM call whose raw JSON
// output is validated server-side against the closed constraint
// grammar before it reaches the caller. Invalid output surfaces as
// [ErrUnparseable] and the public AI.RESPONSE.PARSE_FAILED error
// code.
//
// The closed grammar lives in apps/api/internal/constraint; this
// package is a thin LLM wrapper around constraint.Parse and owns no
// evaluation logic.
package nlconstraint

import (
	"context"
	"errors"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/constraint"
)

// ErrUnparseable is returned whenever the LLM output cannot be
// coerced into a valid constraint expression.
var ErrUnparseable = errors.New("nlconstraint: unparseable")

// Provider abstracts the LLM call. Implementations return the raw
// JSON bytes the model emitted; the Compiler runs constraint.Parse
// on those bytes.
type Provider interface {
	CompileConstraint(ctx context.Context, prompt string) ([]byte, error)
}

// Compiler turns prose into a constraint AST via a Provider plus
// server-side validation.
type Compiler struct {
	Provider Provider
}

// New constructs a Compiler. Panics if provider is nil.
func New(p Provider) *Compiler {
	if p == nil {
		panic("nlconstraint.New: provider must be non-nil")
	}
	return &Compiler{Provider: p}
}

// Compile runs the LLM call and validates the response against the
// closed constraint grammar. Any parse / validation failure is
// mapped to ErrUnparseable so the HTTP layer can return a single
// stable error code.
func (c *Compiler) Compile(ctx context.Context, prompt string) (*constraint.Constraint, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return nil, ErrUnparseable
	}
	raw, err := c.Provider.CompileConstraint(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	parsed, verr := constraint.Parse(raw)
	if verr != nil {
		return nil, ErrUnparseable
	}
	return &parsed, nil
}

// Normalize lowercases + collapses whitespace so fixture keys and
// the mock lookup agree on what "the same prompt" means. Mirrors
// nlquery.Normalize so both packages share fixture conventions.
func Normalize(prompt string) string {
	return strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
}
