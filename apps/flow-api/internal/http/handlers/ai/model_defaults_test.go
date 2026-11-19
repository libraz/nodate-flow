package ai

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

func TestDefaultsForCurrentOfficialModels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind      string
		context   uint32
		maxOutput uint32
		input     int64
		output    int64
	}{
		{"anthropic", 1_000_000, 128_000, 2_000_000, 10_000_000},
		{"openai", 1_050_000, 128_000, 100_000, 500_000},
		{"google", 1_048_576, 65_536, 1_500_000, 7_500_000},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			model := providers.OfficialDefaultModel(providers.Kind(tc.kind))
			got := defaultsForModel(tc.kind, model)
			if got.contextWindow != tc.context || got.maxOutputTokens != tc.maxOutput {
				t.Fatalf("defaultsForModel(%q, %q) = context %d / output %d, want %d / %d",
					tc.kind, model, got.contextWindow, got.maxOutputTokens, tc.context, tc.maxOutput)
			}
			if got.inputPriceMicroUSDPerMTok != tc.input || got.outputPriceMicroUSDPerMTok != tc.output {
				t.Fatalf("defaultsForModel(%q, %q) prices = %d / %d, want %d / %d",
					tc.kind, model, got.inputPriceMicroUSDPerMTok, got.outputPriceMicroUSDPerMTok, tc.input, tc.output)
			}
			if !got.supportsTools || !got.supportsVision {
				t.Fatalf("defaultsForModel(%q, %q) lost supported capabilities: %+v", tc.kind, model, got)
			}
		})
	}
}

func TestResolveDefaultModelForProviderCreate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind      string
		requested string
		want      string
	}{
		{"anthropic", "", "claude-sonnet-5-5"},
		{"openai", "", "gpt-6-luna"},
		{"google", "", "gemini-4-hydrogen"},
		{"ollama", "", ""},
		{"openai_compat", "", ""},
		{"openai", "gpt-4o-mini", "gpt-4o-mini"},
		{"openai_compat", "gpt-6-luna", "gpt-6-luna"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.requested, func(t *testing.T) {
			if got := resolveDefaultModel(tc.kind, tc.requested); got != tc.want {
				t.Fatalf("resolveDefaultModel(%q, %q) = %q, want %q", tc.kind, tc.requested, got, tc.want)
			}
		})
	}
}

func TestExplicitModelsKeepPriorGenericMetadata(t *testing.T) {
	t.Parallel()

	got := defaultsForModel("anthropic", "claude-sonnet-4-6")
	if got.contextWindow != 200_000 || got.maxOutputTokens != 8_192 {
		t.Fatalf("explicit legacy model metadata = context %d / output %d, want 200000 / 8192",
			got.contextWindow, got.maxOutputTokens)
	}
	if got.inputPriceMicroUSDPerMTok != 0 || got.outputPriceMicroUSDPerMTok != 0 {
		t.Fatalf("unlisted explicit model received guessed prices: %d / %d",
			got.inputPriceMicroUSDPerMTok, got.outputPriceMicroUSDPerMTok)
	}
}
