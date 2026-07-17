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

// ErrBaseURLNotAllowed is returned by New when a kind=openai provider
// carries a non-empty base URL. The openai kind targets the official
// api.openai.com endpoint only; a custom base URL (Azure, proxy, gateway)
// must be configured under kind=openai_compat instead. Failing loud here
// prevents the setting from being silently dropped and traffic plus the
// API key from being sent to the official endpoint against the admin's
// intent.
var ErrBaseURLNotAllowed = errors.New("ai/providers: base url is not allowed for kind=openai; use kind=openai_compat")

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
		return &anthropicProvider{cfg: cfg, dec: dec, endpoint: cfg.BaseURL}, nil
	case KindOpenAI:
		// The openai kind is the official endpoint only. Reject a configured
		// base URL rather than silently ignoring it, so an admin who meant to
		// reach a custom endpoint is told to use kind=openai_compat instead of
		// having their traffic and key routed to api.openai.com.
		if cfg.BaseURL != "" {
			return nil, ErrBaseURLNotAllowed
		}
		return &openAIProvider{cfg: cfg, dec: dec, baseURL: defaultOpenAIBaseURL}, nil
	case KindGoogle:
		if err := validateBaseURL(cfg.BaseURL); err != nil {
			return nil, err
		}
		return &googleProvider{cfg: cfg, dec: dec, baseURL: cfg.BaseURL}, nil
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
