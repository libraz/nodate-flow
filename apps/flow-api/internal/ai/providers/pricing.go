package providers

import "strings"

// modelPrice holds per-million-token pricing in millionths of a USD.
type modelPrice struct {
	inputMicrosPerMTok  int64
	outputMicrosPerMTok int64
}

// knownPrices maps model name prefixes to pricing. Entries are checked by
// prefix so "claude-3-5-sonnet" matches "claude-3-5-sonnet-20241022" and
// similar versioned variants. Order matters: longer prefixes first. Public
// provider prices are volatile; reconcile these estimates against provider
// invoice exports before treating them as billing-grade amounts. Unknown
// models gracefully return 0.
//
// Values are millionths of a USD per million tokens (input / output).
var knownPrices = []struct {
	prefix string
	price  modelPrice
}{
	// Anthropic (platform.claude.com/docs/en/about-claude/pricing)
	{"claude-opus-4", modelPrice{5_000_000, 25_000_000}},     // $5 / $25
	{"claude-sonnet-4", modelPrice{3_000_000, 15_000_000}},   // $3 / $15
	{"claude-haiku-4", modelPrice{1_000_000, 5_000_000}},     // $1 / $5
	{"claude-3-5-sonnet", modelPrice{3_000_000, 15_000_000}}, // $3 / $15
	{"claude-3-5-haiku", modelPrice{800_000, 4_000_000}},     // $0.80 / $4
	{"claude-3-opus", modelPrice{15_000_000, 75_000_000}},    // $15 / $75 (legacy)
	{"claude-3-sonnet", modelPrice{3_000_000, 15_000_000}},   // $3 / $15 (legacy)
	{"claude-3-haiku", modelPrice{250_000, 1_250_000}},       // $0.25 / $1.25 (legacy)

	// OpenAI (openai.com/api/pricing)
	{"o4-mini", modelPrice{1_100_000, 4_400_000}},       // $1.10 / $4.40
	{"o3-mini", modelPrice{1_100_000, 4_400_000}},       // $1.10 / $4.40
	{"o3", modelPrice{2_000_000, 8_000_000}},            // $2 / $8
	{"o1-mini", modelPrice{3_000_000, 12_000_000}},      // $3 / $12
	{"o1", modelPrice{15_000_000, 60_000_000}},          // $15 / $60
	{"gpt-4.1-mini", modelPrice{400_000, 1_600_000}},    // $0.40 / $1.60
	{"gpt-4.1-nano", modelPrice{100_000, 400_000}},      // $0.10 / $0.40
	{"gpt-4.1", modelPrice{2_000_000, 8_000_000}},       // $2 / $8
	{"gpt-4o-mini", modelPrice{150_000, 600_000}},       // $0.15 / $0.60
	{"gpt-4o", modelPrice{2_500_000, 10_000_000}},       // $2.50 / $10
	{"gpt-4-turbo", modelPrice{10_000_000, 30_000_000}}, // $10 / $30 (legacy)
	{"gpt-3.5-turbo", modelPrice{500_000, 1_500_000}},   // $0.50 / $1.50 (legacy)

	// Google (ai.google.dev/gemini-api/docs/pricing)
	{"gemini-3.1-pro", modelPrice{2_000_000, 12_000_000}},   // $2 / $12 (≤200k ctx)
	{"gemini-2.5-pro", modelPrice{1_250_000, 10_000_000}},   // $1.25 / $10 (≤200k ctx)
	{"gemini-2.5-flash-lite", modelPrice{100_000, 400_000}}, // $0.10 / $0.40
	{"gemini-2.5-flash", modelPrice{150_000, 380_000}},      // $0.15 / $0.38 (thinking off, ≤200k)
	{"gemini-2.0-flash", modelPrice{100_000, 400_000}},      // $0.10 / $0.40
	{"gemini-1.5-pro", modelPrice{1_250_000, 5_000_000}},    // $1.25 / $5 (≤128k ctx)
	{"gemini-1.5-flash", modelPrice{75_000, 300_000}},       // $0.075 / $0.30 (≤128k ctx)
}

// EstimateCostMicrosUSD returns a best-effort cost in millionths of a US
// dollar for the given model and token counts. Returns 0 when the model is
// unknown or local (Ollama).
func EstimateCostMicrosUSD(model string, inputTokens, outputTokens int) int64 {
	lower := strings.ToLower(model)
	for _, entry := range knownPrices {
		if strings.HasPrefix(lower, entry.prefix) {
			in := int64(inputTokens) * entry.price.inputMicrosPerMTok
			out := int64(outputTokens) * entry.price.outputMicrosPerMTok
			return (in + out) / 1_000_000
		}
	}
	return 0
}

// estimateCostCents returns a whole-cent cost for legacy metrics and memo
// fields. The precise ai_invocations audit path recomputes
// [EstimateCostMicrosUSD] from token counts before persistence.
func estimateCostCents(model string, inputTokens, outputTokens int) int64 {
	return EstimateCostMicrosUSD(model, inputTokens, outputTokens) / 10_000
}
