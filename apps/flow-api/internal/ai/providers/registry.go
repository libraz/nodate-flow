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

// AllKinds returns every provider kind [New] can build, in a stable
// order. It exists so tests can assert the completion contract holds for
// the whole set rather than for whichever kinds someone remembered to
// list: adding a kind to [New] without adding it here leaves the switch
// below unable to build it, and adding it here without covering it in
// the contract test fails that test.
func AllKinds() []Kind {
	return []Kind{KindAnthropic, KindOpenAI, KindGoogle, KindOllama, KindOpenAICompat}
}

// New constructs a Provider for cfg, decrypting nothing yet. The actual
// decryption happens inside Complete, scoped to a single upstream HTTP
// call. dec is the only object that holds the master key; it MUST be the
// process-wide *crypto.Cipher built from NF_SECRET_KEY.
func New(cfg Config, dec Decryptor) (Provider, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	switch cfg.Kind {
	case KindAnthropic:
		return &anthropicProvider{cfg: cfg, dec: dec, endpoint: cfg.BaseURL}, nil
	case KindOpenAI:
		return &openAIProvider{cfg: cfg, dec: dec, baseURL: defaultOpenAIBaseURL}, nil
	case KindGoogle:
		return &googleProvider{cfg: cfg, dec: dec, baseURL: cfg.BaseURL}, nil
	case KindOllama:
		return &ollamaProvider{cfg: cfg}, nil
	case KindOpenAICompat:
		return &openAIProvider{cfg: cfg, dec: dec, baseURL: chooseBaseURL(cfg.BaseURL, defaultOpenAIBaseURL)}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownKind, cfg.Kind)
}

// Validate reports whether cfg describes a provider that can be built.
// [New] calls it first, so anything that passes here is something New
// will accept — the two cannot disagree about what a valid provider is.
//
// It exists separately so the create and update handlers can reject a
// bad configuration at the point the admin submits it. They could not
// call New: it wants a Decryptor, and at submit time there is nothing to
// decrypt. Without this, a kind=openai row carrying a base URL was
// written happily and only failed later, at the first completion, in a
// place with no way to say which field was wrong.
func Validate(cfg Config) error {
	if cfg.Kind != KindOllama && len(cfg.EncryptedKey) == 0 {
		return ErrMissingKey
	}
	switch cfg.Kind {
	case KindAnthropic, KindOllama, KindOpenAICompat, KindGoogle:
		// Every kind that accepts a custom endpoint is judged by the same
		// rule. Applying it to one kind is the same as not having it: the
		// kinds whose whole purpose is a custom endpoint are the ones an
		// admin would actually point somewhere.
		return validateBaseURL(cfg.BaseURL)
	case KindOpenAI:
		// The openai kind is the official endpoint only. Reject a configured
		// base URL rather than silently ignoring it, so an admin who meant to
		// reach a custom endpoint is told to use kind=openai_compat instead of
		// having their traffic and key routed to api.openai.com.
		if cfg.BaseURL != "" {
			return ErrBaseURLNotAllowed
		}
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownKind, cfg.Kind)
}

// chooseBaseURL returns override if non-empty, otherwise fallback. Centralised
// so each provider's New path stays one line.
func chooseBaseURL(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}
