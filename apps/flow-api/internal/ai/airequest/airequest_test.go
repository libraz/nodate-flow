package airequest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// stubProvider stands in for a resolved workspace provider. Only Model
// matters here; Complete is never called.
type stubProvider struct{ model string }

func (p stubProvider) Name() string         { return "stub" }
func (p stubProvider) Kind() providers.Kind { return providers.Kind("stub") }
func (p stubProvider) Model() string        { return p.model }
func (p stubProvider) Complete(context.Context, providers.Request) (*providers.Response, error) {
	return nil, nil
}

func u16(v uint16) *uint16 { return &v }

// TestNewNamesTheProvidersModel covers the metrics half of the defect:
// a request that names no model produced an empty model label on every
// Prometheus AI invocation and cost sample, so spend could not be
// attributed to a model at all.
func TestNewNamesTheProvidersModel(t *testing.T) {
	t.Parallel()

	req := New(stubProvider{model: "claude-sonnet-4-6"}, Args{System: "sys", Prompt: "usr"})
	if req.Model != "claude-sonnet-4-6" {
		t.Fatalf("Model = %q, want the provider's default model", req.Model)
	}
	if req.System != "sys" || req.Prompt != "usr" {
		t.Fatalf("prompts not carried through: %+v", req)
	}
	if req.Temperature != nil {
		t.Fatalf("a call with no agent behind it must not send a temperature; got %v", *req.Temperature)
	}
}

func TestNewToleratesNoProvider(t *testing.T) {
	t.Parallel()

	if req := New(nil, Args{Prompt: "p"}); req.Model != "" {
		t.Fatalf("Model = %q, want empty for a nil provider", req.Model)
	}
}

// TestForAgentPrefersTheAgentsOwnSettings is the core of the defect.
// An agent bound to a cheap model ran on whatever the workspace's
// default provider was configured with — routinely a model an order of
// magnitude more expensive — while the settings screen kept showing the
// model the operator had picked.
func TestForAgentPrefersTheAgentsOwnSettings(t *testing.T) {
	t.Parallel()

	prov := stubProvider{model: "claude-opus-4"}
	req := ForAgent(prov, AgentSettings{
		ModelName:       "gpt-4o-mini",
		MaxOutputTokens: 4096,
		TemperatureX100: u16(20),
	}, Args{System: "sys", Prompt: "usr", MaxTokens: 512})

	if req.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want the agent's model, not the workspace default", req.Model)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want the agent's configured cap", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Errorf("Temperature = %v, want 0.2 (stored as 20, hundredths)", req.Temperature)
	}
}

// TestForAgentFallsBackToCallSiteAndProvider pins what an unconfigured
// agent inherits: the provider's model and the call site's own cap.
func TestForAgentFallsBackToCallSiteAndProvider(t *testing.T) {
	t.Parallel()

	req := ForAgent(stubProvider{model: "gemini-2.5-flash"}, AgentSettings{}, Args{MaxTokens: 512})
	if req.Model != "gemini-2.5-flash" {
		t.Errorf("Model = %q, want the provider default when the agent names none", req.Model)
	}
	if req.MaxTokens != 512 {
		t.Errorf("MaxTokens = %d, want the call site's cap when the agent has none", req.MaxTokens)
	}
	if req.Temperature != nil {
		t.Errorf("Temperature = %v, want none when no agent row was read", *req.Temperature)
	}
}

// TestForAgentSendsAZeroTemperature guards the reason TemperatureX100 is
// a pointer: zero is a real setting (deterministic sampling), and a
// plain uint16 would make it indistinguishable from "unset".
func TestForAgentSendsAZeroTemperature(t *testing.T) {
	t.Parallel()

	req := ForAgent(stubProvider{model: "m"}, AgentSettings{TemperatureX100: u16(0)}, Args{})
	if req.Temperature == nil {
		t.Fatal("an agent configured for temperature 0 must send 0, not omit the field")
	}
	if *req.Temperature != 0 {
		t.Fatalf("Temperature = %v, want 0", *req.Temperature)
	}
}

// TestFromExecRowCarriesEveryColumnTheQuerySelects checks the projection
// the two agent runners share. Each of these columns was already being
// selected and then discarded.
func TestFromExecRowCarriesEveryColumnTheQuerySelects(t *testing.T) {
	t.Parallel()

	s := FromExecRow(generated.GetAgentForExecRow{
		ModelName:       "gpt-4o-mini",
		Temperature:     35,
		MaxOutputTokens: sql.NullInt32{Int32: 2048, Valid: true},
	})
	if s.ModelName != "gpt-4o-mini" {
		t.Errorf("ModelName = %q", s.ModelName)
	}
	if s.MaxOutputTokens != 2048 {
		t.Errorf("MaxOutputTokens = %d, want 2048", s.MaxOutputTokens)
	}
	if s.TemperatureX100 == nil || *s.TemperatureX100 != 35 {
		t.Errorf("TemperatureX100 = %v, want 35", s.TemperatureX100)
	}
}

// TestFromExecRowTreatsNullMaxTokensAsNoCap pins that a NULL column
// means "the model decides", not "cap the response at zero tokens".
func TestFromExecRowTreatsNullMaxTokensAsNoCap(t *testing.T) {
	t.Parallel()

	s := FromExecRow(generated.GetAgentForExecRow{MaxOutputTokens: sql.NullInt32{}})
	if s.MaxOutputTokens != 0 {
		t.Fatalf("MaxOutputTokens = %d, want 0 for a NULL column", s.MaxOutputTokens)
	}
	req := ForAgent(stubProvider{model: "m"}, s, Args{MaxTokens: 777})
	if req.MaxTokens != 777 {
		t.Fatalf("MaxTokens = %d; a NULL agent cap must leave the call site's cap standing", req.MaxTokens)
	}
}

// TestWithPromptKeepsEverySetting covers the retry path: re-asking the
// question must not quietly change the model, cap, or temperature the
// first attempt ran under.
func TestWithPromptKeepsEverySetting(t *testing.T) {
	t.Parallel()

	first := ForAgent(stubProvider{model: "workspace-default"}, AgentSettings{
		ModelName:       "agent-model",
		MaxOutputTokens: 1234,
		TemperatureX100: u16(70),
	}, Args{System: "sys", Prompt: "first"})

	retry := WithPrompt(first, "second")
	if retry.Prompt != "second" {
		t.Fatalf("Prompt = %q, want the new prompt", retry.Prompt)
	}
	if retry.Model != first.Model || retry.MaxTokens != first.MaxTokens || retry.System != first.System {
		t.Fatalf("retry lost settings: first=%+v retry=%+v", first, retry)
	}
	if retry.Temperature == nil || *retry.Temperature != *first.Temperature {
		t.Fatalf("retry lost the temperature: %v", retry.Temperature)
	}
}
