// Package providers contains the LLM provider abstraction (Anthropic /
// OpenAI / Google / Ollama / OpenAI-compatible) and is the ONLY package
// outside internal/crypto allowed to import internal/crypto.
//
// The depguard rule in apps/api/.golangci.yml enforces this. Plaintext API
// keys decrypted here MUST flow straight into the upstream HTTP request's
// Authorization header and never enter long-lived storage, slog records,
// errors, or HTTP responses.
package providers
