package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// wireCase describes, for one provider kind, how to answer a completion
// and where in the outbound JSON that kind spells the three settings a
// caller can choose.
type wireCase struct {
	// respBody is a minimal successful upstream response for the kind,
	// reporting inputTokens/outputTokens of usage.
	respBody string
	// model / maxTokens / temperature pull each setting back out of the
	// decoded request body. A nil result means "the field was absent".
	model       func(body map[string]any) any
	maxTokens   func(body map[string]any) any
	temperature func(body map[string]any) any
	// free marks a kind whose inference the operator is not billed for.
	free bool
}

const (
	wireInputTokens  = 1000
	wireOutputTokens = 500
)

// nested walks a decoded JSON object along path, returning nil if any
// step is missing. Keeps the per-kind extractors to one line each.
func nested(body map[string]any, path ...string) any {
	var cur any = body
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[key]
		if !ok {
			return nil
		}
	}
	return cur
}

// wireCases covers every kind whose endpoint can be pointed at a test
// server. KindOpenAI is absent because its constructor pins the official
// api.openai.com endpoint by design (see ErrBaseURLNotAllowed); it runs
// the same openAIProvider code that KindOpenAICompat exercises here.
// TestWireCasesCoverEveryKind keeps that the only gap.
var wireCases = map[Kind]wireCase{
	KindAnthropic: {
		respBody: `{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1000,"output_tokens":500}}`,
		model:    func(b map[string]any) any { return nested(b, "model") },
		maxTokens: func(b map[string]any) any {
			return nested(b, "max_tokens")
		},
		temperature: func(b map[string]any) any { return nested(b, "temperature") },
	},
	KindOpenAICompat: {
		respBody:    `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500}}`,
		model:       func(b map[string]any) any { return nested(b, "model") },
		maxTokens:   func(b map[string]any) any { return nested(b, "max_tokens") },
		temperature: func(b map[string]any) any { return nested(b, "temperature") },
	},
	KindGoogle: {
		respBody: `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":500}}`,
		// Gemini names the model in the URL path, not the body; the
		// response assertion below covers it.
		model:       func(map[string]any) any { return nil },
		maxTokens:   func(b map[string]any) any { return nested(b, "generationConfig", "maxOutputTokens") },
		temperature: func(b map[string]any) any { return nested(b, "generationConfig", "temperature") },
	},
	KindOllama: {
		respBody:    `{"response":"hi","prompt_eval_count":1000,"eval_count":500}`,
		model:       func(b map[string]any) any { return nested(b, "model") },
		maxTokens:   func(b map[string]any) any { return nested(b, "options", "num_predict") },
		temperature: func(b map[string]any) any { return nested(b, "options", "temperature") },
		free:        true,
	},
}

// TestWireCasesCoverEveryKind fails when a provider kind is added to
// [AllKinds] without an entry above. The contract below is only as good
// as its coverage, and the defects it pins — a dropped output cap, a
// dropped temperature, a guessed price — were each present in exactly
// one provider while its siblings were fine.
func TestWireCasesCoverEveryKind(t *testing.T) {
	t.Parallel()

	for _, kind := range AllKinds() {
		if kind == KindOpenAI {
			continue // shares openAIProvider with KindOpenAICompat; see wireCases
		}
		if _, ok := wireCases[kind]; !ok {
			t.Errorf("provider kind %q has no wire-contract case; add one so its request and cost reporting are covered", kind)
		}
	}
}

// bodyCapturingServer answers every request with body and records the
// decoded request body and URL path.
func bodyCapturingServer(t *testing.T, body string, got *map[string]any, path *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		*got = decoded
		*path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRequestSettingsReachTheUpstreamCall asserts that the model,
// output cap, and temperature a caller chose are actually present in
// the outbound HTTP body.
//
// Carrying them as far as providers.Request is not the same as sending
// them: Gemini spells both knobs inside a nested generationConfig block
// and simply had no code to emit it, so an agent's output cap and
// temperature were dropped on the floor with nothing to notice it. This
// test reads the wire, not the struct.
func TestRequestSettingsReachTheUpstreamCall(t *testing.T) {
	t.Parallel()

	const (
		wantModel = "contract-test-model"
		wantMax   = 4096
	)
	wantTemp := 0.25

	for kind, tc := range wireCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			var body map[string]any
			var path string
			srv := bodyCapturingServer(t, tc.respBody, &body, &path)

			p := newProviderForTest(t, kind, srv.URL)
			resp, err := p.Complete(context.Background(), Request{
				Model:       wantModel,
				Prompt:      "hi",
				MaxTokens:   wantMax,
				Temperature: &wantTemp,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}

			if got := tc.maxTokens(body); got != float64(wantMax) {
				t.Errorf("output cap did not reach the wire: got %v, want %d (body=%v)", got, wantMax, body)
			}
			if got := tc.temperature(body); got != wantTemp {
				t.Errorf("temperature did not reach the wire: got %v, want %v (body=%v)", got, wantTemp, body)
			}
			if got := tc.model(body); got != nil && got != wantModel {
				t.Errorf("model did not reach the wire: got %v, want %q", got, wantModel)
			}
			// Whether it rode in the body or the URL, the response has to
			// name the model the call actually ran on — it is the label
			// every invocation record and cost metric is attributed by.
			if resp.Model != wantModel {
				t.Errorf("Response.Model = %q, want %q", resp.Model, wantModel)
			}
		})
	}
}

// TestOmittedSettingsAreNotSentAsZero pins the other half: a caller that
// chose no temperature must leave the model's own default alone rather
// than pinning it to 0, which is a real and very different setting.
func TestOmittedSettingsAreNotSentAsZero(t *testing.T) {
	t.Parallel()

	for kind, tc := range wireCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			var body map[string]any
			var path string
			srv := bodyCapturingServer(t, tc.respBody, &body, &path)

			p := newProviderForTest(t, kind, srv.URL)
			if _, err := p.Complete(context.Background(), Request{Model: "m", Prompt: "hi"}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got := tc.temperature(body); got != nil {
				t.Errorf("no temperature was chosen, but %v was sent (body=%v)", got, body)
			}
		})
	}
}

// TestProviderReportsItsOwnCost asserts each provider prices its own
// call, and that a provider running locally reports zero rather than
// leaving the number to be guessed downstream.
//
// The guess was not harmless: a local model name is absent from the
// price table, and unpriced models deliberately fall back to the highest
// rate in it, so free inference was recorded — and charged against the
// workspace's daily budget — at the most expensive rate the product
// knows.
func TestProviderReportsItsOwnCost(t *testing.T) {
	t.Parallel()

	for kind, tc := range wireCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			var body map[string]any
			var path string
			srv := bodyCapturingServer(t, tc.respBody, &body, &path)

			// A model name absent from the price table, so the
			// conservative fallback is what a downstream guess would
			// produce.
			const model = "some-unlisted-local-model"
			p := newProviderForTest(t, kind, srv.URL)
			resp, err := p.Complete(context.Background(), Request{Model: model, Prompt: "hi"})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.InputTokens != wireInputTokens || resp.OutputTokens != wireOutputTokens {
				t.Fatalf("usage not parsed: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
			}

			guess := EstimateCostMicrosUSD(model, wireInputTokens, wireOutputTokens)
			if guess <= 0 {
				t.Fatal("test premise broken: an unlisted model must estimate above zero, " +
					"otherwise the free-provider assertion below proves nothing")
			}
			if tc.free {
				if resp.EstimatedCostMicros() != 0 {
					t.Errorf("local inference must report zero cost, got %d micro-USD", resp.EstimatedCostMicros())
				}
				return
			}
			if resp.EstimatedCostMicros() != guess {
				t.Errorf("cost = %d, want the provider's own estimate %d", resp.EstimatedCostMicros(), guess)
			}
		})
	}
}

// TestProviderModelIsNeverEmpty asserts every provider can name the
// model it will use before the call is made. A failed call still has to
// be logged and metered against a model, and there is no response to
// read one off.
func TestProviderModelIsNeverEmpty(t *testing.T) {
	t.Parallel()

	for _, kind := range AllKinds() {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			cfg := Config{Kind: kind, Name: "n", EncryptedKey: []byte("sealed-key")}
			if kind == KindOllama {
				cfg.EncryptedKey = nil
			}
			p, err := New(cfg, fakeDecryptor{})
			if err != nil {
				t.Fatalf("New(%s): %v", kind, err)
			}
			if p.Model() == "" {
				t.Errorf("kind %q reports no default model", kind)
			}

			cfg.DefaultModel = "configured-model"
			configured, err := New(cfg, fakeDecryptor{})
			if err != nil {
				t.Fatalf("New(%s) with default model: %v", kind, err)
			}
			if configured.Model() != "configured-model" {
				t.Errorf("kind %q ignores ai_providers.default_model: got %q", kind, configured.Model())
			}
		})
	}

	if m := NewMockProvider("").Model(); m == "" {
		t.Error("the mock provider must name a model too, or every test running under NF_AI_MOCK " +
			"would accept an empty model label")
	}
}
