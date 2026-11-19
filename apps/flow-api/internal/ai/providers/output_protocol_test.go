package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const testOutputSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string","minLength":1},"priority":{"type":"integer","minimum":1}},"required":["title","priority"],"additionalProperties":false}`

func testOutputSpec(t *testing.T) *OutputSpec {
	t.Helper()
	spec, err := NewOutputSpec("task-output", []byte(testOutputSchema))
	if err != nil {
		t.Fatalf("NewOutputSpec: %v", err)
	}
	return spec
}

type protocolResult struct {
	kind            Kind
	body            string
	wantTermination Termination
	wantReason      string
	wantText        string
	wantMode        OutputMode
}

func TestProviderCompletionProtocolClassification(t *testing.T) {
	validJSON := `{"title":"task","priority":2}`
	quotedJSON, err := json.Marshal(validJSON)
	if err != nil {
		t.Fatal(err)
	}
	cases := []protocolResult{
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationComplete, "stop", "hello", OutputModeValidatedText},
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":` + string(quotedJSON) + `},"finish_reason":"length"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationIncomplete, "length", validJSON, OutputModeValidatedText},
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationRefused, "content_filter", "", OutputModeValidatedText},
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":"no","refusal":"safety"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationRefused, "refusal", "no", OutputModeValidatedText},
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1"}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationIncomplete, "tool_calls", "", OutputModeValidatedText},
		{KindOpenAICompat, `{"choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`, TerminationUnknown, "", "hello", OutputModeValidatedText},

		{KindAnthropic, `{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`, TerminationComplete, "end_turn", "hello", OutputModeValidatedText},
		{KindAnthropic, `{"content":[{"type":"text","text":` + string(quotedJSON) + `}],"stop_reason":"max_tokens","usage":{"input_tokens":2,"output_tokens":3}}`, TerminationIncomplete, "max_tokens", validJSON, OutputModeValidatedText},
		{KindAnthropic, `{"content":[{"type":"text","text":"I cannot help with that."}],"stop_reason":"refusal","usage":{"input_tokens":2,"output_tokens":3}}`, TerminationRefused, "refusal", "I cannot help with that.", OutputModeValidatedText},
		{KindAnthropic, `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":2,"output_tokens":3}}`, TerminationRefused, "refusal", "", OutputModeValidatedText},
		{KindAnthropic, `{"content":[{"type":"text","text":"hello"}],"stop_reason":"stop_sequence","usage":{"input_tokens":2,"output_tokens":3}}`, TerminationUnknown, "stop_sequence", "hello", OutputModeValidatedText},
		{KindAnthropic, `{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":2,"output_tokens":3}}`, TerminationUnknown, "", "hello", OutputModeValidatedText},

		{KindGoogle, `{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationComplete, "stop", "hello", OutputModeValidatedText},
		{KindGoogle, `{"candidates":[{"content":{"parts":[{"text":"thought","thought":true},{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationComplete, "stop", "answer", OutputModeValidatedText},
		{KindGoogle, `{"candidates":[{"content":{"parts":[{"text":` + string(quotedJSON) + `}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationIncomplete, "max_tokens", validJSON, OutputModeValidatedText},
		{KindGoogle, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationRefused, "safety", "", OutputModeValidatedText},
		{KindGoogle, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationIncomplete, "function_call", "", OutputModeValidatedText},
		{KindGoogle, `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationRefused, "safety", "", OutputModeValidatedText},
		{KindGoogle, `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`, TerminationUnknown, "", "hello", OutputModeValidatedText},

		{KindOllama, `{"response":"hello","done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":3}`, TerminationComplete, "stop", "hello", OutputModeNativeSchema},
		{KindOllama, `{"response":` + string(quotedJSON) + `,"done":true,"done_reason":"length","prompt_eval_count":2,"eval_count":3}`, TerminationIncomplete, "length", validJSON, OutputModeNativeSchema},
		{KindOllama, `{"response":"hello","done":true,"prompt_eval_count":2,"eval_count":3}`, TerminationUnknown, "", "hello", OutputModeNativeSchema},
		{KindOllama, `{"response":"hello","done":false,"done_reason":"stop","prompt_eval_count":2,"eval_count":3}`, TerminationUnknown, "stop", "hello", OutputModeNativeSchema},
	}

	for i, tc := range cases {
		t.Run(string(tc.kind)+"/"+string(tc.wantTermination)+"/"+tc.wantReason+"/"+strconv.Itoa(i), func(t *testing.T) {
			var calls atomic.Int32
			requestBodies := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				decoded := map[string]any{}
				_ = json.Unmarshal(raw, &decoded)
				requestBodies <- decoded
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			p := newProviderForTest(t, tc.kind, srv.URL)
			resp, err := p.Complete(context.Background(), Request{Model: "model-under-test", Prompt: "return structured data", Output: testOutputSpec(t)})
			if err != nil {
				t.Fatalf("Complete returned HTTP/envelope error for decoded generation: %v", err)
			}
			requestBody := <-requestBodies
			if resp.Termination != tc.wantTermination || resp.StopReason != tc.wantReason {
				t.Errorf("termination/reason = %q/%q, want %q/%q", resp.Termination, resp.StopReason, tc.wantTermination, tc.wantReason)
			}
			if resp.Text != tc.wantText {
				t.Errorf("text = %q, want %q", resp.Text, tc.wantText)
			}
			if resp.OutputMode != tc.wantMode {
				t.Errorf("output mode = %q, want %q", resp.OutputMode, tc.wantMode)
			}
			wantCompletionErr := error(ErrResponseSchemaMismatch)
			switch tc.wantTermination {
			case TerminationComplete:
				if strings.TrimSpace(tc.wantText) != "" {
					wantCompletionErr = nil
				}
			case TerminationIncomplete, TerminationUnknown:
				wantCompletionErr = ErrGenerationIncomplete
			case TerminationRefused:
				wantCompletionErr = ErrModelRefused
			}
			if got := RequireComplete(resp); !errors.Is(got, wantCompletionErr) || (got == nil) != (wantCompletionErr == nil) {
				t.Errorf("RequireComplete err = %v, want %v", got, wantCompletionErr)
			}
			if resp.InputTokens != 2 || resp.OutputTokens != 3 {
				t.Errorf("usage lost on classified response: input=%d output=%d", resp.InputTokens, resp.OutputTokens)
			}
			wantCost := EstimateCostMicrosUSD("model-under-test", 2, 3)
			if tc.kind == KindOllama {
				wantCost = 0
			}
			if got := resp.EstimatedCostMicros(); got != wantCost {
				t.Errorf("cost = %d, want %d", got, wantCost)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("upstream call count = %d, want exactly one", got)
			}
			if tc.kind == KindOllama {
				if _, ok := requestBody["format"]; !ok {
					t.Errorf("verified loopback Ollama request omitted native schema format: %v", requestBody)
				}
			} else {
				for _, key := range []string{"response_format", "output_config", "format"} {
					if _, ok := requestBody[key]; ok {
						t.Errorf("unverified %s request unexpectedly sent native schema field %q: %v", tc.kind, key, requestBody)
					}
				}
			}
		})
	}
}

func TestAnthropicNativeSchemaFormatEnvelope(t *testing.T) {
	spec := testOutputSpec(t)
	mode, config := anthropicOutputConfigFor("claude-opus-5-5", anthropicEndpoint, spec)
	if mode != OutputModeNativeSchema || config == nil {
		t.Fatalf("native capability returned mode=%q config=%v", mode, config)
	}
	body, err := json.Marshal(struct {
		OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	}{OutputConfig: config})
	if err != nil {
		t.Fatalf("marshal output config: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode output config: %v", err)
	}
	outputConfig, ok := got["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("Anthropic request omitted output_config: %#v", got)
	}
	format, ok := outputConfig["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["schema"] == nil {
		t.Fatalf("wrong Anthropic structured output envelope: %#v", got)
	}
}

// TestFreeFormObjectSchemaFallsBackToValidatedText pins that an object
// without declared properties is never projected to the empty-object-only
// shape a native dialect would then enforce.
func TestFreeFormObjectSchemaFallsBackToValidatedText(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		wantMode OutputMode
	}{
		{"nested free-form payload", `{"type":"object","properties":{"proposed_events":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string"},"payload_json":{"type":"object"}},"required":["type","payload_json"],"additionalProperties":false}}},"required":["proposed_events"],"additionalProperties":false}`, OutputModeValidatedText},
		{"free-form root", `{"type":"object"}`, OutputModeValidatedText},
		{"explicitly empty object", `{"type":"object","additionalProperties":false}`, OutputModeNativeSchema},
		{"declared properties", testOutputSchema, OutputModeNativeSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := NewOutputSpec("open-object", []byte(tc.schema))
			if err != nil {
				t.Fatalf("NewOutputSpec: %v", err)
			}
			mode, native := nativeSchemaFor(KindAnthropic, "claude-opus-5-5", anthropicEndpoint, spec)
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q (projected %s)", mode, tc.wantMode, native)
			}
			if (native != nil) != (tc.wantMode == OutputModeNativeSchema) {
				t.Fatalf("native schema = %s for mode %q", native, mode)
			}
		})
	}
}

func TestOpenAINativeSchemaWireAndCompleteGuard(t *testing.T) {
	var calls atomic.Int32
	requestBodies := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		requestBodies <- decoded
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"title\":\"task\",\"priority\":2}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`)
	}))
	t.Cleanup(srv.Close)

	// KindOpenAI cannot be configured with a custom URL in production. This
	// package-local instance substitutes a loopback server while retaining
	// the official endpoint identity that New guarantees for this kind.
	p := &openAIProvider{cfg: Config{Kind: KindOpenAI, Name: "test-openai"}, dec: fakeDecryptor{}, baseURL: srv.URL}
	resp, err := p.Complete(context.Background(), Request{Model: "gpt-4o-mini", Prompt: "return structured data", Output: testOutputSpec(t)})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	requestBody := <-requestBodies
	format, ok := requestBody["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("verified OpenAI request omitted response_format: %v", requestBody)
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok || format["type"] != "json_schema" || jsonSchema["strict"] != true || jsonSchema["name"] != "task-output" {
		t.Fatalf("wrong OpenAI structured output envelope: %#v", format)
	}
	if resp.OutputMode != OutputModeNativeSchema {
		t.Errorf("OutputMode = %q, want native_schema", resp.OutputMode)
	}
	if calls.Load() != 1 {
		t.Errorf("call count = %d, want one", calls.Load())
	}
	if err := RequireComplete(resp); err != nil {
		t.Errorf("complete nonempty response rejected: %v", err)
	}
}

func TestRequireCompleteFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		resp *Response
		want error
	}{
		{"nil", nil, ErrResponseSchemaMismatch},
		{"zero-value termination", &Response{Text: "valid-looking"}, ErrGenerationIncomplete},
		{"incomplete", &Response{Text: `{"valid":true}`, Termination: TerminationIncomplete}, ErrGenerationIncomplete},
		{"refused with text", &Response{Text: "cannot help", Termination: TerminationRefused}, ErrModelRefused},
		{"refused without text", &Response{Termination: TerminationRefused}, ErrModelRefused},
		{"complete empty", &Response{Termination: TerminationComplete}, ErrResponseSchemaMismatch},
		{"unknown", &Response{Text: "valid-looking", Termination: TerminationUnknown}, ErrGenerationIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RequireComplete(tc.resp); !errors.Is(err, tc.want) {
				t.Fatalf("RequireComplete err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestOpenAICompatibleOutputFallsBackBeforeSingleCall(t *testing.T) {
	var calls atomic.Int32
	requestBodies := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		requestBodies <- decoded
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	}))
	t.Cleanup(srv.Close)

	p := newProviderForTest(t, KindOpenAICompat, srv.URL)
	resp, err := p.Complete(context.Background(), Request{Model: "gpt-4o-mini", Prompt: "p", Output: testOutputSpec(t)})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	requestBody := <-requestBodies
	if _, ok := requestBody["response_format"]; ok {
		t.Fatalf("OpenAI-compatible endpoint received an unverified native format: %v", requestBody)
	}
	if resp.OutputMode != OutputModeValidatedText {
		t.Errorf("OutputMode = %q, want validated_text", resp.OutputMode)
	}
	if calls.Load() != 1 {
		t.Errorf("fallback call count = %d, want one; schema fallback must happen before the call", calls.Load())
	}
}

func TestOpenAISchemaProjectionFallsBackBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	requestBodies := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		requestBodies <- decoded
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	}))
	t.Cleanup(srv.Close)

	optionalSchema, err := NewOutputSpec("optional-output", []byte(`{"type":"object","properties":{"optional":{"type":"string"}},"additionalProperties":false}`))
	if err != nil {
		t.Fatalf("NewOutputSpec: %v", err)
	}
	p := &openAIProvider{cfg: Config{Kind: KindOpenAI, Name: "test-openai"}, dec: fakeDecryptor{}, baseURL: srv.URL}
	resp, err := p.Complete(context.Background(), Request{Model: "gpt-4o-mini", Prompt: "p", Output: optionalSchema})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	requestBody := <-requestBodies
	if _, ok := requestBody["response_format"]; ok {
		t.Fatalf("strict OpenAI schema with optional property should fall back before request: %v", requestBody)
	}
	if resp.OutputMode != OutputModeValidatedText {
		t.Errorf("OutputMode = %q, want validated_text", resp.OutputMode)
	}
	if calls.Load() != 1 {
		t.Errorf("fallback call count = %d, want one", calls.Load())
	}
}

func TestProviderMalformedEnvelopeRemainsUpstreamError(t *testing.T) {
	cases := []struct {
		kind Kind
		body string
	}{
		{KindOpenAICompat, `{"choices":[]}`},
		{KindAnthropic, `{"content":[]}`},
		{KindGoogle, `{"candidates":[]}`},
		{KindOllama, `not-json`},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			p := newProviderForTest(t, tc.kind, srv.URL)
			if _, err := p.Complete(context.Background(), Request{Prompt: "p"}); err == nil {
				t.Fatal("Complete succeeded for malformed/missing response envelope")
			} else if !errors.Is(err, ErrResponseSchemaMismatch) && !errors.Is(err, ErrResponseInvalidJSON) {
				t.Fatalf("Complete err = %v, want an envelope sentinel", err)
			}
		})
	}
}

// TestEmptyGenerationKeepsUpstreamUsage pins that a billed exchange with no
// candidate still returns its usage alongside the envelope error.
func TestEmptyGenerationKeepsUpstreamUsage(t *testing.T) {
	cases := []struct {
		kind Kind
		body string
	}{
		{KindOpenAICompat, `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}`},
		{KindGoogle, `{"candidates":[],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			p := newProviderForTest(t, tc.kind, srv.URL)
			resp, err := p.Complete(context.Background(), Request{Model: "model-under-test", Prompt: "p"})
			if !errors.Is(err, ErrResponseSchemaMismatch) {
				t.Fatalf("Complete err = %v, want ErrResponseSchemaMismatch", err)
			}
			if resp == nil {
				t.Fatal("Complete dropped the response, so upstream usage is lost")
			}
			if resp.InputTokens != 2 || resp.OutputTokens != 3 {
				t.Errorf("usage = %d/%d, want 2/3", resp.InputTokens, resp.OutputTokens)
			}
			if got, want := resp.EstimatedCostMicros(), EstimateCostMicrosUSD("model-under-test", 2, 3); got != want {
				t.Errorf("cost = %d, want %d", got, want)
			}
		})
	}
}

func TestNativeOutputCapabilitiesAreExactAndConservative(t *testing.T) {
	tests := []struct {
		kind     Kind
		model    string
		endpoint string
		verified bool
		dialect  OutputDialect
	}{
		{KindOpenAI, "gpt-6-luna", defaultOpenAIBaseURL, true, DialectOpenAIChatCompletions},
		{KindOpenAI, "gpt-4o-mini", defaultOpenAIBaseURL, true, DialectOpenAIChatCompletions},
		{KindOpenAI, "gpt-4o-mini-2024-07-18", defaultOpenAIBaseURL, true, DialectOpenAIChatCompletions},
		{KindOpenAI, "gpt-4o-2024-08-06", defaultOpenAIBaseURL, true, DialectOpenAIChatCompletions},
		{KindOpenAI, "gpt-4o-mini-preview", defaultOpenAIBaseURL, false, ""},
		{KindOpenAICompat, "gpt-4o-mini", "http://127.0.0.1:9000/v1", false, ""},
		{KindAnthropic, "claude-sonnet-5-5", anthropicEndpoint, true, DialectAnthropicMessages},
		{KindAnthropic, "claude-opus-5-5", anthropicEndpoint, true, DialectAnthropicMessages},
		{KindAnthropic, "claude-3-5-sonnet-latest", anthropicEndpoint, false, ""},
		{KindGoogle, "gemini-2.5-flash", googleBaseURL, false, ""},
		{KindGoogle, "gemini-4-hydrogen", googleBaseURL, true, DialectGoogleGenerate},
		{KindGoogle, "gemini-4-hydrogen", "http://127.0.0.1:9000/v1", false, ""},
		{KindOllama, "llama3.2", "http://localhost:11434", true, DialectOllamaGenerate},
		{KindOllama, "llama3.2", "http://ollama.example:11434", false, ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind)+"/"+tc.model, func(t *testing.T) {
			got := NativeOutputCapability(tc.kind, tc.model, tc.endpoint)
			if got.Verified != tc.verified || got.Dialect != tc.dialect {
				t.Fatalf("capability = %+v, want verified=%t dialect=%q", got, tc.verified, tc.dialect)
			}
			if got.Reason == "" {
				t.Fatal("capability decision must include its bounded reason")
			}
		})
	}
}

func TestStopReasonIsBounded(t *testing.T) {
	got := boundedStopReason(strings.Repeat("A", 1000) + "!? secret")
	if len(got) > maxStopReasonLength {
		t.Fatalf("stop reason length = %d, want at most %d", len(got), maxStopReasonLength)
	}
	if strings.ContainsAny(got, "!? ") {
		t.Fatalf("stop reason contains unbounded characters: %q", got)
	}
}
