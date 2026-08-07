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
//
// Nothing outside apps/flow-api/internal/ai/airequest may build one: the
// per-agent model, output cap, and temperature reach the upstream call
// only if every field is filled from the same place, and hand-built
// literals lost them silently. See that package for the rationale and
// for the guard test that enforces it.
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
	// Temperature is the sampling temperature to send upstream. Nil means
	// "do not send one" — the upstream model's own default applies. A
	// pointer rather than a float so that a deliberate 0 (fully
	// deterministic sampling) stays distinguishable from "unset"; a plain
	// float64 would make the zero value silently request greedy decoding.
	Temperature *float64
}

// Response is a minimal LLM call result.
type Response struct {
	Model        string
	Text         string
	InputTokens  int
	OutputTokens int
	// CostMicros is what the call cost, in millionths of a US dollar, as
	// computed by the provider that made it. It is authoritative: a
	// provider that charges nothing reports zero, and downstream code
	// records that zero rather than re-deriving a price from the model
	// name. Estimating downstream is how free local inference came to be
	// logged at the most expensive rate in the price table.
	CostMicros int64
	// CostCents is a legacy whole-cent estimate kept for older call sites
	// and tests. New code should use [Response.EstimatedCostMicros] and
	// round only at display boundaries.
	CostCents int64
}

// EstimatedCostMicros returns the precise micro-USD estimate when present,
// falling back to the legacy whole-cent field for older tests and adapters.
func (r *Response) EstimatedCostMicros() int64 {
	if r == nil {
		return 0
	}
	if r.CostMicros > 0 {
		return r.CostMicros
	}
	return r.CostCents * 10_000
}

// EstimatedCostCents rounds the estimate down to whole cents for legacy
// display-only fields that still carry "cents" in their storage name.
func (r *Response) EstimatedCostCents() int64 {
	return r.EstimatedCostMicros() / 10_000
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

	// Model returns the model this provider calls when Request.Model is
	// empty: the configured ai_providers.default_model, or the kind's
	// built-in fallback. Callers need it before the call is made, because
	// a failed call still has to be logged and metered against a model
	// name, and reading it off the (absent) response leaves the label
	// blank.
	Model() string

	// Complete performs a single completion call against the upstream LLM.
	Complete(ctx context.Context, req Request) (*Response, error)
}
