package providers

import "testing"

func TestEstimateCostMicrosUSDTracksSubCentDefaultModels(t *testing.T) {
	t.Parallel()

	micros := EstimateCostMicrosUSD("gpt-4o-mini", 1000, 500)
	if micros != 450 {
		t.Fatalf("EstimateCostMicrosUSD(gpt-4o-mini, 1000, 500) = %d, want 450", micros)
	}
	if cents := estimateCostCents("gpt-4o-mini", 1000, 500); cents != 0 {
		t.Fatalf("estimateCostCents remains whole-cent floor for legacy metrics = %d, want 0", cents)
	}
}

func TestResponseEstimatedCostUsesMicrosBeforeLegacyCents(t *testing.T) {
	t.Parallel()

	subCent := &Response{CostMicros: 450}
	if got := subCent.EstimatedCostMicros(); got != 450 {
		t.Fatalf("EstimatedCostMicros() = %d, want 450", got)
	}
	if got := subCent.EstimatedCostCents(); got != 0 {
		t.Fatalf("EstimatedCostCents() floors display cents = %d, want 0", got)
	}

	legacy := &Response{CostCents: 2}
	if got := legacy.EstimatedCostMicros(); got != 20_000 {
		t.Fatalf("legacy EstimatedCostMicros() = %d, want 20000", got)
	}
	if got := legacy.EstimatedCostCents(); got != 2 {
		t.Fatalf("legacy EstimatedCostCents() = %d, want 2", got)
	}
}

func TestEstimateCostMicrosUSDHandlesFractionalCentPerMillionPricing(t *testing.T) {
	t.Parallel()

	if got := EstimateCostMicrosUSD("gemini-1.5-flash-latest", 1_000_000, 0); got != 75_000 {
		t.Fatalf("gemini-1.5-flash input price = %d micros, want 75000", got)
	}
	if got := EstimateCostMicrosUSD("gemini-1.5-flash-latest", 0, 1_000_000); got != 300_000 {
		t.Fatalf("gemini-1.5-flash output price = %d micros, want 300000", got)
	}
}

func TestEstimateCostMicrosUSDChargesUnknownModelsConservatively(t *testing.T) {
	t.Parallel()

	// Models routed through openai_compat gateways (LiteLLM/vLLM/OpenRouter/
	// LM Studio) or newer than the price table must not cost 0, otherwise the
	// budget and quota guards go silent.
	for _, model := range []string{
		"litellm/some-proxied-model",
		"openrouter/anthropic/claude-future",
		"gpt-9-supernova",
		"local-model",
		"",
	} {
		got := EstimateCostMicrosUSD(model, 1_000, 1_000)
		if got <= 0 {
			t.Fatalf("unknown model %q priced at %d micros, want nonzero conservative cost", model, got)
		}
	}

	// The fallback must equal the highest known input/output rate so it never
	// undercounts relative to any table entry.
	want := (int64(1_000)*conservativePrice.inputMicrosPerMTok + int64(1_000)*conservativePrice.outputMicrosPerMTok) / 1_000_000
	if got := EstimateCostMicrosUSD("totally-unknown-model", 1_000, 1_000); got != want {
		t.Fatalf("unknown model cost = %d micros, want conservative %d", got, want)
	}
	if conservativePrice.inputMicrosPerMTok <= 0 || conservativePrice.outputMicrosPerMTok <= 0 {
		t.Fatalf("conservative price must be positive, got %+v", conservativePrice)
	}
}

func TestEstimateCostMicrosUSDCoversRuntimeDefaultModels(t *testing.T) {
	t.Parallel()

	for _, model := range []string{
		openAIDefaultModel,
		anthropicDefaultMdl,
		googleDefaultModel,
		"claude-sonnet-4-6",
	} {
		if got := EstimateCostMicrosUSD(model, 1_000, 1_000); got <= 0 {
			t.Fatalf("runtime default model %q has no positive price", model)
		}
	}
}
