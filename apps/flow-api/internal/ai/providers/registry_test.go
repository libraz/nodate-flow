package providers

import (
	"errors"
	"testing"
)

// TestOpenAIRejectsBaseURL asserts that kind=openai fails loud at
// construction when a base URL is configured. The openai kind targets the
// official api.openai.com endpoint only, so a custom base URL must be
// rejected (directing the admin to kind=openai_compat) rather than being
// silently dropped and the traffic plus key routed to api.openai.com.
func TestOpenAIRejectsBaseURL(t *testing.T) {
	t.Parallel()

	cases := []string{
		"https://my-azure-openai.example.com/v1",
		"https://proxy.internal/v1",
		"http://localhost:8080/v1",
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := New(Config{
				Kind:         KindOpenAI,
				Name:         "o",
				BaseURL:      raw,
				EncryptedKey: []byte("sealed-key"),
			}, fakeDecryptor{})
			if !errors.Is(err, ErrBaseURLNotAllowed) {
				t.Fatalf("New(kind=openai, base=%q) err = %v, want ErrBaseURLNotAllowed", raw, err)
			}
		})
	}
}

// TestOpenAIAcceptsEmptyBaseURL asserts that kind=openai still constructs
// successfully when no base URL is configured, pinning the provider to the
// official default endpoint.
func TestOpenAIAcceptsEmptyBaseURL(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		Kind:         KindOpenAI,
		Name:         "o",
		BaseURL:      "",
		EncryptedKey: []byte("sealed-key"),
	}, fakeDecryptor{})
	if err != nil {
		t.Fatalf("New(kind=openai, base=\"\"): %v", err)
	}
	if got := p.Kind(); got != KindOpenAI {
		t.Fatalf("provider kind = %q, want %q", got, KindOpenAI)
	}
}

// TestOpenAICompatAcceptsBaseURL asserts that a custom base URL remains
// valid under kind=openai_compat, which is the intended home for
// Azure/proxy/gateway endpoints.
func TestOpenAICompatAcceptsBaseURL(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		Kind:         KindOpenAICompat,
		Name:         "c",
		BaseURL:      "https://proxy.internal/v1",
		EncryptedKey: []byte("sealed-key"),
	}, fakeDecryptor{})
	if err != nil {
		t.Fatalf("New(kind=openai_compat, base=custom): %v", err)
	}
	if got := p.Kind(); got != KindOpenAICompat {
		t.Fatalf("provider kind = %q, want %q", got, KindOpenAICompat)
	}
}

// TestValidateAgreesWithNew pins the contract the create handler relies
// on: anything Validate accepts, New accepts, and anything Validate
// rejects, New rejects with the same sentinel. The handler cannot call
// New — there is no sealed key to hand it at submit time — so the two
// have to be the same rule rather than two rules that look alike.
func TestValidateAgreesWithNew(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
	}{
		{"openai with a base url", Config{Kind: KindOpenAI, BaseURL: "https://proxy/v1", EncryptedKey: []byte("k")}},
		{"openai without one", Config{Kind: KindOpenAI, EncryptedKey: []byte("k")}},
		{"openai_compat with a base url", Config{Kind: KindOpenAICompat, BaseURL: "https://proxy/v1", EncryptedKey: []byte("k")}},
		{"anthropic", Config{Kind: KindAnthropic, EncryptedKey: []byte("k")}},
		{"google with a malformed base url", Config{Kind: KindGoogle, BaseURL: "ftp://nope", EncryptedKey: []byte("k")}},
		{"google with a good base url", Config{Kind: KindGoogle, BaseURL: "https://gen.example/v1", EncryptedKey: []byte("k")}},
		{"ollama without a key", Config{Kind: KindOllama}},
		{"a kind nothing implements", Config{Kind: Kind("nope"), EncryptedKey: []byte("k")}},
		{"a key-requiring kind with no key", Config{Kind: KindAnthropic}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vErr := Validate(tc.cfg)
			_, nErr := New(tc.cfg, fakeDecryptor{})
			if (vErr == nil) != (nErr == nil) {
				t.Fatalf("Validate err = %v but New err = %v", vErr, nErr)
			}
			if vErr != nil && nErr != nil && vErr.Error() != nErr.Error() {
				t.Fatalf("Validate err = %v, New err = %v; want the same", vErr, nErr)
			}
		})
	}
}
