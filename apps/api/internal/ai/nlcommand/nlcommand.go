// Package nlcommand resolves natural language commands into MCP tool
// invocations. A Resolver turns user prose into a validated ToolCall
// using a closed tool whitelist. The LLM returns JSON with tool name,
// args, and confidence; validation ensures the tool is in the allowed
// set before the caller dispatches to the MCP handler.
package nlcommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnresolvable is the sentinel returned whenever the LLM output
// cannot be mapped to an allowed tool invocation. Callers map this to
// the public AI.NL_COMMAND.UNRESOLVABLE error code.
var ErrUnresolvable = errors.New("nlcommand: unresolvable")

// allowedTools is the closed set of MCP tools that may be invoked via
// natural language commands. Tools that require complex multi-step
// flows (e.g. apply_steps, propose_duplicates) are excluded.
var allowedTools = map[string]struct{}{
	"create_task":       {},
	"update_task":       {},
	"search_tasks":      {},
	"propose_lens":      {},
	"add_comment":       {},
	"list_tasks":        {},
	"list_projects":     {},
	"smart_create_task": {},
}

// ToolCall is the validated result of resolving a natural language
// command. Tool is guaranteed to be in the allowed set.
type ToolCall struct {
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	Confidence float64        `json:"confidence"`
}

// ToolSpec describes one MCP tool for the LLM prompt so it can pick
// the right tool and shape the args correctly.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Provider abstracts the LLM call. Implementations return the raw JSON
// bytes the model emitted (typically via function-calling or structured
// output). The Resolver validates those bytes.
type Provider interface {
	ResolveCommand(ctx context.Context, prompt string, tools []ToolSpec) ([]byte, error)
}

// Resolver turns prose into a ToolCall via a Provider plus server-side
// validation against the allowed tools whitelist.
type Resolver struct {
	Provider Provider
	Tools    []ToolSpec
}

// New constructs a Resolver. Panics if provider is nil.
func New(p Provider, tools []ToolSpec) *Resolver {
	if p == nil {
		panic("nlcommand.New: provider must be non-nil")
	}
	return &Resolver{Provider: p, Tools: tools}
}

// Resolve runs the single-round LLM call and validates the response
// against the allowed tools whitelist. Returns ErrUnresolvable for any
// invalid, empty, or unrecognized output.
func (r *Resolver) Resolve(ctx context.Context, prompt string) (*ToolCall, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return nil, ErrUnresolvable
	}
	raw, err := r.Provider.ResolveCommand(ctx, trimmed, r.Tools)
	if err != nil {
		return nil, err
	}
	tc, verr := ValidateBytes(raw, allowedTools)
	if verr != nil {
		return nil, ErrUnresolvable
	}
	return tc, nil
}

// ValidateBytes parses and validates raw JSON against the allowed tools
// set. Exported so tests and the mock provider can reuse the exact
// validation path.
func ValidateBytes(raw []byte, allowed map[string]struct{}) (*ToolCall, error) {
	var candidate struct {
		Tool       string         `json:"tool"`
		Args       map[string]any `json:"args"`
		Confidence float64        `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if candidate.Tool == "" {
		return nil, errors.New("empty tool name")
	}
	if _, ok := allowed[candidate.Tool]; !ok {
		return nil, fmt.Errorf("tool %q not in allowed set", candidate.Tool)
	}
	if candidate.Confidence < 0.0 || candidate.Confidence > 1.0 {
		return nil, fmt.Errorf("confidence %f out of range [0.0, 1.0]", candidate.Confidence)
	}
	if candidate.Args == nil {
		candidate.Args = map[string]any{}
	}
	return &ToolCall{
		Tool:       candidate.Tool,
		Args:       candidate.Args,
		Confidence: candidate.Confidence,
	}, nil
}

// Normalize lowercases + collapses whitespace so fixture keys and the
// mock lookup agree on what "the same prompt" means.
func Normalize(prompt string) string {
	return strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
}
