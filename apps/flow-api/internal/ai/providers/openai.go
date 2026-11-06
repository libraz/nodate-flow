package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	openAIDefaultModel   = "gpt-4o-mini"
)

// openAIProvider also serves the openai_compat kind: any endpoint that
// speaks the OpenAI Chat Completions schema (LiteLLM, vLLM, OpenRouter).
type openAIProvider struct {
	cfg     Config
	dec     Decryptor
	baseURL string
}

func (p *openAIProvider) Name() string { return p.cfg.Name }
func (p *openAIProvider) Kind() Kind   { return p.cfg.Kind }
func (p *openAIProvider) Model() string {
	return chooseBaseURL(p.cfg.DefaultModel, openAIDefaultModel)
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIReq struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	// Temperature is omitted when the caller did not choose one, leaving
	// the model's own default in force.
	Temperature    *float64              `json:"temperature,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIResponseFormat struct {
	Type       string                 `json:"type"`
	JSONSchema openAIJSONSchemaFormat `json:"json_schema"`
}

type openAIJSONSchemaFormat struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openAIResp struct {
	Choices []openAIChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type openAIChoice struct {
	Message      openAIResponseMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

type openAIResponseMessage struct {
	Content      string            `json:"content"`
	Refusal      *string           `json:"refusal"`
	ToolCalls    []json.RawMessage `json:"tool_calls"`
	FunctionCall json.RawMessage   `json:"function_call"`
}

func (p *openAIProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.Model()
	}
	msgs := make([]openAIMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, openAIMessage{Role: "user", Content: req.Prompt})

	modeEndpoint := p.baseURL
	// New rejects a custom endpoint for KindOpenAI, so production instances
	// of that kind always target the official API. Keeping the configured
	// identity here also lets this package's wire tests substitute httptest.
	if p.cfg.Kind == KindOpenAI {
		modeEndpoint = defaultOpenAIBaseURL
	}
	outputMode, nativeSchema := nativeSchemaFor(p.cfg.Kind, model, modeEndpoint, req.Output)
	var responseFormat *openAIResponseFormat
	if outputMode == OutputModeNativeSchema {
		responseFormat = &openAIResponseFormat{
			Type: "json_schema",
			JSONSchema: openAIJSONSchemaFormat{
				Name:   req.Output.Contract(),
				Strict: true,
				Schema: nativeSchema,
			},
		}
	}
	body, err := json.Marshal(openAIReq{
		Model:          model,
		Messages:       msgs,
		MaxTokens:      req.MaxTokens,
		Temperature:    req.Temperature,
		ResponseFormat: responseFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}

	url := strings.TrimRight(p.baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("openai: decrypt key: %w: %w", ErrKeyDecryptFailed, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+string(plain))
	zero(plain)

	resp, err := doLimited(ctx, DestOpenAI, httpReq)
	if err != nil {
		return nil, classifyTransportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, classifyTransportError(ctx, err)
	}
	if uerr := classifyHTTPStatus(resp.StatusCode, resp.Header.Get("Retry-After")); uerr != nil {
		return nil, uerr
	}

	var or openAIResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, &UpstreamError{Sentinel: ErrResponseInvalidJSON, Status: resp.StatusCode}
	}
	if or.Error != nil {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: resp.StatusCode}
	}
	costMicros := EstimateCostMicrosUSD(model, or.Usage.PromptTokens, or.Usage.CompletionTokens)
	out := &Response{
		Model:        model,
		OutputMode:   outputMode,
		InputTokens:  or.Usage.PromptTokens,
		OutputTokens: or.Usage.CompletionTokens,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}
	// The exchange was billed even without a choice, so usage goes back with the error.
	if len(or.Choices) == 0 {
		return out, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	choice := or.Choices[0]
	out.Text = choice.Message.Content
	out.Termination, out.StopReason = classifyOpenAICompletion(choice)
	return out, nil
}

func classifyOpenAICompletion(choice openAIChoice) (Termination, string) {
	if choice.Message.Refusal != nil {
		return TerminationRefused, "refusal"
	}
	reason := boundedStopReason(choice.FinishReason)
	switch strings.ToLower(choice.FinishReason) {
	case "content_filter", "refusal":
		return TerminationRefused, reason
	}
	if len(choice.Message.ToolCalls) > 0 || (len(choice.Message.FunctionCall) > 0 && string(choice.Message.FunctionCall) != "null") {
		if reason == "" {
			reason = "tool_calls"
		}
		return TerminationIncomplete, reason
	}
	switch strings.ToLower(choice.FinishReason) {
	case "stop":
		return TerminationComplete, reason
	case "length":
		return TerminationIncomplete, reason
	case "tool_calls", "function_call":
		return TerminationIncomplete, reason
	default:
		return TerminationUnknown, reason
	}
}
