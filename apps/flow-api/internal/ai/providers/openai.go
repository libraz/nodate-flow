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

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIReq struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIResp struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (p *openAIProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = chooseBaseURL(p.cfg.DefaultModel, openAIDefaultModel)
	}
	msgs := make([]openAIMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, openAIMessage{Role: "user", Content: req.Prompt})

	body, err := json.Marshal(openAIReq{
		Model:     model,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
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
		return nil, fmt.Errorf("openai: decrypt key: %w", err)
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
	if len(or.Choices) == 0 {
		return nil, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	text := or.Choices[0].Message.Content
	costMicros := EstimateCostMicrosUSD(model, or.Usage.PromptTokens, or.Usage.CompletionTokens)
	return &Response{
		Model:        model,
		Text:         text,
		InputTokens:  or.Usage.PromptTokens,
		OutputTokens: or.Usage.CompletionTokens,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}, nil
}
