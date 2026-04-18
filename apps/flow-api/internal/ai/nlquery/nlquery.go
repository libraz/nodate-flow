// Package nlquery implements the NL query → Lens JSON compiler from
// ADR 0004. A Compiler turns user prose into a strictly-typed Lens
// object using a closed field/operator whitelist. The LLM is expected
// to return JSON that matches the Lens grammar; anything else is a
// validation error and surfaces as AI.NL_QUERY.UNPARSEABLE.
//
// The closed grammar is the security boundary (ADR 0004 §5). No raw
// SQL, no wildcard fields, no OR combinator. The caller is responsible
// for scoping the compiled Lens to the authenticated workspace before
// execution.
package nlquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnparseable is the sentinel returned whenever the LLM output
// cannot be coerced into a valid Lens. Callers map this to the public
// AI.NL_QUERY.UNPARSEABLE error code.
var ErrUnparseable = errors.New("nlquery: unparseable")

// Field is a whitelisted Lens filter field. No other field name may
// appear in the compiled Lens (ADR 0004 §2).
type Field string

const (
	FieldTitle       Field = "title"
	FieldStatus      Field = "status"
	FieldAssignee    Field = "assignee"
	FieldProject     Field = "project"
	FieldWorkspace   Field = "workspace"
	FieldPriority    Field = "priority"
	FieldEstimate    Field = "estimate"
	FieldCreatedAt   Field = "created_at"
	FieldUpdatedAt   Field = "updated_at"
	FieldDueOn       Field = "due_on"
	FieldBlocked     Field = "blocked"
	FieldLabels      Field = "labels"
	FieldHasEstimate Field = "has_estimate"
)

// allowedFields is the whitelist enforced server-side after the LLM
// responds. Order matches ADR 0004 appendix A.
var allowedFields = map[Field]struct{}{
	FieldTitle: {}, FieldStatus: {}, FieldAssignee: {}, FieldProject: {},
	FieldWorkspace: {}, FieldPriority: {}, FieldEstimate: {},
	FieldCreatedAt: {}, FieldUpdatedAt: {}, FieldDueOn: {},
	FieldBlocked: {}, FieldLabels: {}, FieldHasEstimate: {},
}

// allowedOperators enumerates the closed operator set. "between"
// accepts a token (e.g. "this_week") rather than an array for Lens v1.
var allowedOperators = map[string]struct{}{
	"eq": {}, "neq": {}, "in": {}, "nin": {}, "gt": {}, "gte": {},
	"lt": {}, "lte": {}, "contains": {}, "between": {},
	"is_null": {}, "is_not_null": {},
}

// allowedSortFields matches the sort.field enum from ADR 0004.
var allowedSortFields = map[string]struct{}{
	"title": {}, "status": {}, "assignee": {}, "project": {},
	"priority": {}, "estimate": {}, "created_at": {},
	"updated_at": {}, "due_on": {},
}

// allowedGroupBy is the closed groupBy enum; the empty string / JSON
// null is also allowed.
var allowedGroupBy = map[string]struct{}{
	"status": {}, "assignee": {}, "project": {}, "priority": {}, "labels": {},
}

// Lens is the compiled, validated filter descriptor. It mirrors the
// existing saved-lens shape used by the task list / board / timeline.
type Lens struct {
	Filter  map[Field]map[string]any `json:"filter"`
	Sort    []SortSpec               `json:"sort"`
	GroupBy *string                  `json:"groupBy"`
}

// SortSpec is one element of Lens.Sort.
type SortSpec struct {
	Field string `json:"field"`
	Dir   string `json:"dir"`
}

// Provider abstracts the LLM call. Implementations return the raw JSON
// bytes the model emitted (typically via function-calling or
// structured output). The Compiler validates those bytes.
type Provider interface {
	CompileLens(ctx context.Context, prompt string) ([]byte, error)
}

// Compiler turns prose into a Lens via a Provider plus server-side
// validation.
type Compiler struct {
	Provider Provider
}

// New constructs a Compiler. Panics if provider is nil.
func New(p Provider) *Compiler {
	if p == nil {
		panic("nlquery.New: provider must be non-nil")
	}
	return &Compiler{Provider: p}
}

// Compile runs the single-round LLM call and validates the response
// against the closed Lens grammar. Returns ErrUnparseable for any
// invalid or malformed output.
func (c *Compiler) Compile(ctx context.Context, prompt string) (*Lens, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return nil, ErrUnparseable
	}
	raw, err := c.Provider.CompileLens(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	lens, verr := ValidateBytes(raw)
	if verr != nil {
		return nil, ErrUnparseable
	}
	return lens, nil
}

// ValidateBytes parses and validates raw JSON against the closed Lens
// grammar. Exported so tests and the mock provider can reuse the exact
// validation path.
func ValidateBytes(raw []byte) (*Lens, error) {
	var candidate struct {
		Filter  map[string]map[string]any `json:"filter"`
		Sort    []SortSpec                `json:"sort"`
		GroupBy *string                   `json:"groupBy"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&candidate); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(candidate.Filter) == 0 {
		return nil, errors.New("empty filter")
	}

	out := &Lens{
		Filter:  make(map[Field]map[string]any, len(candidate.Filter)),
		Sort:    make([]SortSpec, 0, len(candidate.Sort)),
		GroupBy: candidate.GroupBy,
	}

	for k, ops := range candidate.Filter {
		f := Field(k)
		if _, ok := allowedFields[f]; !ok {
			return nil, fmt.Errorf("unknown field %q", k)
		}
		if len(ops) == 0 {
			return nil, fmt.Errorf("field %q has no operator", k)
		}
		for op := range ops {
			if _, ok := allowedOperators[op]; !ok {
				return nil, fmt.Errorf("unknown operator %q on field %q", op, k)
			}
		}
		out.Filter[f] = ops
	}

	for _, s := range candidate.Sort {
		if _, ok := allowedSortFields[s.Field]; !ok {
			return nil, fmt.Errorf("unknown sort field %q", s.Field)
		}
		if s.Dir != "asc" && s.Dir != "desc" {
			return nil, fmt.Errorf("unknown sort dir %q", s.Dir)
		}
		out.Sort = append(out.Sort, s)
	}

	if candidate.GroupBy != nil {
		if _, ok := allowedGroupBy[*candidate.GroupBy]; !ok {
			return nil, fmt.Errorf("unknown groupBy %q", *candidate.GroupBy)
		}
	}
	return out, nil
}

// Normalize lowercases + collapses whitespace so fixture keys and the
// mock lookup agree on what "the same prompt" means.
func Normalize(prompt string) string {
	return strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
}
