package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicSonnet55UsesMigratedWireFields(t *testing.T) {
	t.Parallel()

	temperature := 0.3
	for _, tc := range []struct {
		model           string
		wantThinking    any
		wantTemperature any
	}{
		{model: "claude-sonnet-5-5", wantThinking: "between_tools"},
		{model: "claude-3-5-sonnet-latest", wantTemperature: temperature},
	} {
		t.Run(tc.model, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body := map[string]any{}
				_ = json.Unmarshal(raw, &body)
				bodies <- body
				_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			t.Cleanup(srv.Close)

			p := newProviderForTest(t, KindAnthropic, srv.URL)
			if _, err := p.Complete(context.Background(), Request{
				Model: tc.model, Prompt: "p", MaxTokens: 1024, Temperature: &temperature,
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			body := <-bodies
			if got := nested(body, "model"); got != tc.model {
				t.Errorf("model = %v, want %q", got, tc.model)
			}
			if got := nested(body, "max_tokens"); got != float64(1024) {
				t.Errorf("max_tokens = %v, want 1024", got)
			}
			if got := nested(body, "thinking", "type"); got != tc.wantThinking {
				t.Errorf("thinking.type = %v, want %q", got, tc.wantThinking)
			}
			if got := nested(body, "temperature"); got != tc.wantTemperature {
				t.Errorf("temperature = %v, want %v", got, tc.wantTemperature)
			}
		})
	}
}

func TestOpenAIGPT6LunaUsesModernWireFieldsOnlyForOfficialModel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind                Kind
		model               string
		wantMaxTokens       any
		wantMaxCompletion   any
		wantReasoningEffort any
		wantNativeSchema    bool
	}{
		{
			kind: KindOpenAI, model: "gpt-6-luna", wantMaxCompletion: float64(2048),
			wantReasoningEffort: "none", wantNativeSchema: true,
		},
		{kind: KindOpenAI, model: "gpt-4o-mini", wantMaxTokens: float64(2048), wantNativeSchema: true},
		{kind: KindOpenAICompat, model: "gpt-6-luna", wantMaxTokens: float64(2048)},
	} {
		t.Run(string(tc.kind)+"/"+tc.model, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body := map[string]any{}
				_ = json.Unmarshal(raw, &body)
				bodies <- body
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
			}))
			t.Cleanup(srv.Close)

			p := &openAIProvider{
				cfg: Config{Kind: tc.kind, Name: "test-openai"},
				dec: fakeDecryptor{}, baseURL: srv.URL,
			}
			if _, err := p.Complete(context.Background(), Request{
				Model: tc.model, Prompt: "p", MaxTokens: 2048, Output: testOutputSpec(t),
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			body := <-bodies
			if got := nested(body, "max_tokens"); got != tc.wantMaxTokens {
				t.Errorf("max_tokens = %v, want %v", got, tc.wantMaxTokens)
			}
			if got := nested(body, "max_completion_tokens"); got != tc.wantMaxCompletion {
				t.Errorf("max_completion_tokens = %v, want %v", got, tc.wantMaxCompletion)
			}
			if got := nested(body, "reasoning_effort"); got != tc.wantReasoningEffort {
				t.Errorf("reasoning_effort = %v, want %v", got, tc.wantReasoningEffort)
			}
			_, hasNativeSchema := body["response_format"]
			if hasNativeSchema != tc.wantNativeSchema {
				t.Errorf("native response schema presence = %t, want %t (body=%v)", hasNativeSchema, tc.wantNativeSchema, body)
			}
		})
	}
}

func TestGeminiHydrogenWireAndThoughtUsage(t *testing.T) {
	t.Parallel()

	temperature := 0.3
	bodies := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		bodies <- body
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":2}}`)
	}))
	t.Cleanup(srv.Close)

	p := newProviderForTest(t, KindGoogle, srv.URL)
	resp, err := p.Complete(context.Background(), Request{
		Model: googleDefaultModel, Prompt: "p", MaxTokens: 2048, Temperature: &temperature,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	body := <-bodies
	cfg, ok := nested(body, "generationConfig").(map[string]any)
	if !ok {
		t.Fatalf("generationConfig missing: %v", body)
	}
	if got := cfg["maxOutputTokens"]; got != float64(2048) {
		t.Errorf("maxOutputTokens = %v, want 2048", got)
	}
	if got := nested(cfg, "thinkingConfig", "thinkingLevel"); got != "LOW" {
		t.Errorf("thinkingLevel = %v, want LOW", got)
	}
	if _, ok := cfg["temperature"]; ok {
		t.Errorf("unsupported temperature was sent: %v", cfg)
	}
	if resp.OutputTokens != 5 {
		t.Errorf("output tokens = %d, want candidate plus thought tokens (5)", resp.OutputTokens)
	}
	if got, want := resp.CostMicros, EstimateCostMicrosUSD(googleDefaultModel, 5, 5); got != want {
		t.Errorf("cost = %d micros, want %d including thought tokens", got, want)
	}
}

func TestGeminiHydrogenNativeStructuredOutputEnvelope(t *testing.T) {
	t.Parallel()

	spec := testOutputSpec(t)
	mode, schema := nativeSchemaFor(KindGoogle, googleDefaultModel, googleBaseURL, spec)
	if mode != OutputModeNativeSchema || len(schema) == 0 {
		t.Fatalf("nativeSchemaFor = %q / %s, want native schema", mode, schema)
	}
	encoded, err := json.Marshal(generationConfig(googleDefaultModel, Request{Output: spec}, schema))
	if err != nil {
		t.Fatalf("marshal generationConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(encoded, &cfg); err != nil {
		t.Fatalf("decode generationConfig: %v", err)
	}
	if got := cfg["responseMimeType"]; got != "application/json" {
		t.Errorf("responseMimeType = %v, want application/json", got)
	}
	if got := cfg["responseJsonSchema"]; got == nil {
		t.Errorf("responseJsonSchema missing from generation config: %s", encoded)
	}
	if got := nested(cfg, "thinkingConfig", "thinkingLevel"); got != "LOW" {
		t.Errorf("thinkingLevel = %v, want LOW", got)
	}
}
