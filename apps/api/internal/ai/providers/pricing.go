package providers

import "strings"

// modelPrice holds per-million-token pricing in cents.
type modelPrice struct {
	inputCentsPerMTok  int64
	outputCentsPerMTok int64
}

// knownPrices maps model name prefixes to pricing. Entries are checked
// by prefix so "claude-3-5-sonnet" matches "claude-3-5-sonnet-20241022"
// and similar versioned variants. Order matters: longer prefixes first.
//
// Prices sourced from public provider pages as of 2026-04. Update when
// pricing changes; unknown models gracefully return 0.
//
// Values are USD cents per million tokens (input / output).
var knownPrices = []struct {
	prefix string
	price  modelPrice
}{
	// Anthropic (platform.claude.com/docs/en/about-claude/pricing)
	{"claude-opus-4", modelPrice{500, 2500}},     // $5 / $25
	{"claude-sonnet-4", modelPrice{300, 1500}},    // $3 / $15
	{"claude-haiku-4", modelPrice{100, 500}},      // $1 / $5
	{"claude-3-5-sonnet", modelPrice{300, 1500}},  // $3 / $15
	{"claude-3-5-haiku", modelPrice{80, 400}},     // $0.80 / $4
	{"claude-3-opus", modelPrice{1500, 7500}},     // $15 / $75 (legacy)
	{"claude-3-sonnet", modelPrice{300, 1500}},    // $3 / $15 (legacy)
	{"claude-3-haiku", modelPrice{25, 125}},       // $0.25 / $1.25 (legacy)

	// OpenAI (openai.com/api/pricing)
	{"o4-mini", modelPrice{110, 440}},             // $1.10 / $4.40
	{"o3-mini", modelPrice{110, 440}},             // $1.10 / $4.40
	{"o3", modelPrice{200, 800}},                  // $2 / $8
	{"o1-mini", modelPrice{300, 1200}},            // $3 / $12
	{"o1", modelPrice{1500, 6000}},                // $15 / $60
	{"gpt-4.1-mini", modelPrice{40, 160}},         // $0.40 / $1.60
	{"gpt-4.1-nano", modelPrice{10, 40}},          // $0.10 / $0.40
	{"gpt-4.1", modelPrice{200, 800}},             // $2 / $8
	{"gpt-4o-mini", modelPrice{15, 60}},           // $0.15 / $0.60
	{"gpt-4o", modelPrice{250, 1000}},             // $2.50 / $10
	{"gpt-4-turbo", modelPrice{1000, 3000}},       // $10 / $30 (legacy)
	{"gpt-3.5-turbo", modelPrice{50, 150}},        // $0.50 / $1.50 (legacy)

	// Google (ai.google.dev/gemini-api/docs/pricing)
	{"gemini-3.1-pro", modelPrice{200, 1200}},     // $2 / $12 (≤200k ctx)
	{"gemini-2.5-pro", modelPrice{125, 1000}},     // $1.25 / $10 (≤200k ctx)
	{"gemini-2.5-flash-lite", modelPrice{10, 40}}, // $0.10 / $0.40
	{"gemini-2.5-flash", modelPrice{15, 38}},      // $0.15 / $0.38 (thinking off, ≤200k)
	{"gemini-2.0-flash", modelPrice{10, 40}},      // $0.10 / $0.40
	{"gemini-1.5-pro", modelPrice{125, 500}},      // $1.25 / $5 (≤128k ctx)
	{"gemini-1.5-flash", modelPrice{8, 30}},       // $0.075 / $0.30 (≤128k ctx)
}

// estimateCostCents returns a best-effort cost in cents for the given
// model and token counts. Returns 0 when the model is unknown or
// local (Ollama).
func estimateCostCents(model string, inputTokens, outputTokens int) int64 {
	lower := strings.ToLower(model)
	for _, entry := range knownPrices {
		if strings.HasPrefix(lower, entry.prefix) {
			in := int64(inputTokens) * entry.price.inputCentsPerMTok
			out := int64(outputTokens) * entry.price.outputCentsPerMTok
			return (in + out) / 1_000_000
		}
	}
	return 0
}
