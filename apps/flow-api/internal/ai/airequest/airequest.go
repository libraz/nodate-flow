// Package airequest is the single builder for [providers.Request].
//
// Callers state what they decided — the system prompt, the user prompt,
// and (where the response shape has a known ceiling) an output cap.
// Everything else the request needs but nobody chooses per call site —
// which model to name, and how to carry an agent's configured output cap
// and temperature — is owned here.
//
// The split is the point of the package. Every call site used to build
// providers.Request{System: ..., Prompt: ...} by hand, and the fields
// nobody typed were the fields that silently stopped working:
//
//   - Model was never set, so the request went to whatever model the
//     workspace's default provider happened to be configured with. An
//     agent configured to run on a cheap model ran on the workspace
//     default instead, at that model's price, while the settings screen
//     went on displaying the model the operator had chosen. The same
//     empty field became the model label on every Prometheus AI cost and
//     invocation metric, so the bill it produced could not be attributed
//     either.
//   - MaxTokens was never set for agent runs, so the Anthropic provider
//     applied its own 1024-token floor and long answers were truncated
//     mid-sentence with no error.
//   - Temperature had nowhere to live at all: ai_agents.temperature was
//     read out of the database, carried as far as the runner, and
//     dropped.
//
// Adding those fields to the struct would not have fixed it — a struct
// literal that omits a field still compiles. So the literal itself is
// out of bounds: nothing outside this package may write
// providers.Request{...}, and [TestRequestConstructionCentralized]
// enforces it across the module. A new call site has to pick [New] or
// [ForAgent], and picking [ForAgent] means producing an [AgentSettings],
// which in production only [FromExecRow] can do.
package airequest

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// Args carries what a call site decides about one LLM call.
//
// Model and Temperature are deliberately absent: a call site does not
// choose them, the provider or the agent row does.
type Args struct {
	// System is the system prompt. May be empty.
	System string
	// Prompt is the user message. May be empty for self-directed agent
	// ticks, where the system prompt is the whole instruction.
	Prompt string
	// MaxTokens caps the response for call sites whose parser has a known
	// ceiling (a JSON verdict, a generated page body). Zero means the
	// provider decides. For an agent run it is only the fallback: the
	// agent's own configured cap wins.
	MaxTokens int
}

// AgentSettings is the projection of an ai_agents row that decides how
// its LLM call is made.
//
// Production code must obtain one from [FromExecRow] rather than filling
// the fields in by hand; that is what keeps "the query selects it" and
// "the request carries it" from drifting apart. Tests build them
// directly, which is why the fields are exported.
type AgentSettings struct {
	// ModelName is ai_models.name for the model the agent is bound to.
	// Empty falls back to the provider's default model.
	ModelName string
	// MaxOutputTokens is ai_agents.max_output_tokens. Zero means the
	// column was NULL — no per-agent cap — and [Args.MaxTokens] applies.
	MaxOutputTokens int
	// TemperatureX100 is ai_agents.temperature, the sampling temperature
	// multiplied by 100 as the column stores it. Nil means no agent row
	// was consulted and no temperature is sent at all. It is a pointer
	// because 0 is a meaningful temperature: with a plain uint16, a
	// half-filled AgentSettings would silently request greedy decoding
	// instead of leaving the model's default alone.
	TemperatureX100 *uint16
}

// FromExecRow projects the agent row both agent runners load.
//
// This is the only production path to an [AgentSettings], so widening
// GetAgentForExec is the only way to add a per-agent knob — and a knob
// added to the query but not forwarded here is a compile-visible gap
// rather than a setting that quietly does nothing.
func FromExecRow(row generated.GetAgentForExecRow) AgentSettings {
	temp := row.Temperature
	s := AgentSettings{
		ModelName:       row.ModelName,
		TemperatureX100: &temp,
	}
	if row.MaxOutputTokens.Valid && row.MaxOutputTokens.Int32 > 0 {
		s.MaxOutputTokens = int(row.MaxOutputTokens.Int32)
	}
	return s
}

// New builds the request for a call that has no agent behind it: an
// operator-triggered proposal, a natural-language compile, a page
// generation. The workspace's default provider decides the model.
//
// p may be nil, in which case the model is left empty; callers reject a
// nil provider before reaching the upstream call, and returning a
// zero-valued request keeps this function total.
func New(p providers.Provider, a Args) providers.Request {
	return providers.Request{
		Model:     modelOf(p),
		System:    a.System,
		Prompt:    a.Prompt,
		MaxTokens: a.MaxTokens,
	}
}

// ForAgent builds the request for one agent's LLM call, applying the
// agent's own model, output cap, and temperature over the provider
// defaults.
func ForAgent(p providers.Provider, s AgentSettings, a Args) providers.Request {
	req := New(p, a)
	if s.ModelName != "" {
		req.Model = s.ModelName
	}
	if s.MaxOutputTokens > 0 {
		req.MaxTokens = s.MaxOutputTokens
	}
	if s.TemperatureX100 != nil {
		t := float64(*s.TemperatureX100) / 100
		req.Temperature = &t
	}
	return req
}

// WithPrompt returns req with a different user prompt and every other
// field — model, cap, temperature, system prompt — unchanged. It is how
// a retry re-asks the same question: rebuilding the request from scratch
// is what dropped the agent's settings on the second attempt.
func WithPrompt(req providers.Request, prompt string) providers.Request {
	req.Prompt = prompt
	return req
}

// modelOf returns the provider's effective default model. A nil
// interface is tolerated because every call site rejects an unresolved
// provider before the upstream call, and returning an empty model there
// is more useful than panicking inside the builder.
func modelOf(p providers.Provider) string {
	if p == nil {
		return ""
	}
	return p.Model()
}
