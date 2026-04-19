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

// Config carries everything a Provider needs to talk to its upstream LLM.
// EncryptedKey is the sealed blob from ai_providers.api_key_ciphertext; it
// is decrypted only inside the Provider implementation, used immediately,
// and the resulting plaintext slice is zeroed before the call returns.
type Config struct {
	// Kind selects the upstream protocol.
	Kind Kind
	// Name is a human-readable label for logs and audit. NEVER a secret.
	Name string
	// BaseURL overrides the default upstream endpoint (Ollama, openai_compat).
	BaseURL string
	// DefaultModel is used when Request.Model is empty.
	DefaultModel string
	// EncryptedKey is the sealed AES-GCM blob. Empty for Ollama.
	EncryptedKey []byte
	// APIKeyPrefix is the masked first-8 of the plaintext, safe to log.
	APIKeyPrefix string
}

// Request is a minimal LLM call payload.
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
	// CostCents is the provider's best-effort cost estimate, or zero if the
	// provider does not surface pricing in its response.
	CostCents int64
}

// Decryptor is the narrow contract that providers depend on for unsealing
// stored API keys. Only go-shared/crypto.Cipher satisfies this in production;
// tests may inject a fake.
type Decryptor interface {
	Decrypt(blob []byte) ([]byte, error)
}

// Provider is the abstraction every concrete LLM backend implements. The
// implementation is responsible for decrypting its sealed key, attaching
// the plaintext to the upstream Authorization header, and dropping the
// plaintext immediately.
type Provider interface {
	// Name returns a stable identifier for the provider instance, used in
	// logs and audit records. It MUST NOT include any secret material.
	Name() string

	// Kind returns the provider kind enum value.
	Kind() Kind

	// Complete performs a single completion call against the upstream LLM.
	Complete(ctx context.Context, req Request) (*Response, error)
}
