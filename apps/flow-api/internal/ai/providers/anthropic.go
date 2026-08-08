package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	anthropicEndpoint    = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	anthropicDefaultMdl  = "claude-3-5-sonnet-latest"
	anthropicMaxTokensFb = 1024
)

// anthropicProvider talks to api.anthropic.com using the Messages API.
type anthropicProvider struct {
	cfg      Config
	dec      Decryptor
	endpoint string
}

func (p *anthropicProvider) Name() string { return p.cfg.Name }
func (p *anthropicProvider) Kind() Kind   { return KindAnthropic }
func (p *anthropicProvider) Model() string {
	return chooseBaseURL(p.cfg.DefaultModel, anthropicDefaultMdl)
}

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	// Temperature is omitted when the caller did not choose one, leaving
	// the model's own default in force.
	Temperature *float64 `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *anthropicProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.Model()
	}
	// max_tokens is required by the Messages API, so an absent cap has to
	// become some number here. It is the one place in the provider set
	// where a missing value silently truncates a long answer instead of
	// failing, which is why the agent's configured cap must reach this
	// call rather than being re-defaulted.
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = anthropicMaxTokensFb
	}
	body, err := json.Marshal(anthropicReq{
		Model:       model,
		MaxTokens:   maxTok,
		System:      req.System,
		Messages:    []anthropicMessage{{Role: "user", Content: req.Prompt}},
		Temperature: req.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	endpoint := chooseBaseURL(p.endpoint, anthropicEndpoint)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("anthropic: decrypt key: %w: %w", ErrKeyDecryptFailed, err)
	}
	httpReq.Header.Set("x-api-key", string(plain))
	zero(plain)

	resp, err := doLimited(ctx, DestAnthropic, httpReq)
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

	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, &UpstreamError{Sentinel: ErrResponseInvalidJSON, Status: resp.StatusCode}
	}
	if ar.Error != nil {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: resp.StatusCode}
	}
	if len(ar.Content) == 0 {
		return nil, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	costMicros := EstimateCostMicrosUSD(model, ar.Usage.InputTokens, ar.Usage.OutputTokens)
	return &Response{
		Model:        model,
		Text:         text,
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}, nil
}
