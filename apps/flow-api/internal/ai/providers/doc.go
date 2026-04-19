// Package providers contains the LLM provider abstraction (Anthropic /
// OpenAI / Google / Ollama / OpenAI-compatible) and is the ONLY package
// outside go-shared/crypto allowed to import go-shared/crypto.
//
// The depguard rule in apps/flow-api/.golangci.yml enforces this. Plaintext API
// keys decrypted here MUST flow straight into the upstream HTTP request's
// Authorization header and never enter long-lived storage, slog records,
// errors, or HTTP responses.
package providers
