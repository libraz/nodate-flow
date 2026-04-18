package providers

import (
	"errors"
	"fmt"
)

// ErrUnknownKind is returned by New when cfg.Kind does not map to any
// registered provider.
var ErrUnknownKind = errors.New("ai/providers: unknown provider kind")

// ErrMissingKey is returned by New when a non-Ollama provider has no
// sealed API key blob.
var ErrMissingKey = errors.New("ai/providers: missing encrypted api key")

// New constructs a Provider for cfg, decrypting nothing yet. The actual
// decryption happens inside Complete, scoped to a single upstream HTTP
// call. dec is the only object that holds the master key; it MUST be the
// process-wide *crypto.Cipher built from NF_SECRET_KEY.
func New(cfg Config, dec Decryptor) (Provider, error) {
	if cfg.Kind != KindOllama && len(cfg.EncryptedKey) == 0 {
		return nil, ErrMissingKey
	}
	switch cfg.Kind {
	case KindAnthropic:
		return &anthropicProvider{cfg: cfg, dec: dec}, nil
	case KindOpenAI:
		return &openAIProvider{cfg: cfg, dec: dec, baseURL: defaultOpenAIBaseURL}, nil
	case KindGoogle:
		return &googleProvider{cfg: cfg, dec: dec}, nil
	case KindOllama:
		return &ollamaProvider{cfg: cfg}, nil
	case KindOpenAICompat:
		return &openAIProvider{cfg: cfg, dec: dec, baseURL: chooseBaseURL(cfg.BaseURL, defaultOpenAIBaseURL)}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownKind, cfg.Kind)
}

// chooseBaseURL returns override if non-empty, otherwise fallback. Centralised
// so each provider's New path stays one line.
func chooseBaseURL(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}
