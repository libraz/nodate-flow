package providers

import "context"

// Kind enumerates the supported LLM provider kinds. The string values
// match the ai_providers.kind enum in MySQL and the AiProvidersKind
// values produced by sqlc.
type Kind string

// Provider kinds supported by the abstraction.
const (
	KindAnthropic    Kind = "anthropic"
	KindOpenAI       Kind = "openai"
	KindGoogle       Kind = "google"
	KindOllama       Kind = "ollama"
	KindOpenAICompat Kind = "openai_compat"
)

// Request is a minimal LLM call payload. The full schema (tools, JSON
// mode, temperature, etc.) will land alongside the first concrete provider
// implementation; the stub keeps the interface stable.
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
}

// Response is a minimal LLM call result.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Provider is the abstraction every concrete LLM backend implements. The
// implementation is responsible for fetching the encrypted key from the
// database, calling crypto.Decrypt, attaching the plaintext to the
// upstream Authorization header, and dropping the plaintext immediately.
type Provider interface {
	// Name returns a stable identifier for the provider instance, used in
	// logs and audit records. It MUST NOT include any secret material.
	Name() string

	// Generate performs a single completion call against the upstream LLM.
	Generate(ctx context.Context, req Request) (*Response, error)
}
