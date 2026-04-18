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
	cfg Config
	dec Decryptor
}

func (p *anthropicProvider) Name() string { return p.cfg.Name }
func (p *anthropicProvider) Kind() Kind   { return KindAnthropic }

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
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
		model = chooseBaseURL(p.cfg.DefaultModel, anthropicDefaultMdl)
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = anthropicMaxTokensFb
	}
	body, err := json.Marshal(anthropicReq{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("anthropic: decrypt key: %w", err)
	}
	httpReq.Header.Set("x-api-key", string(plain))
	zero(plain)

	resp, err := doLimited(ctx, DestAnthropic, httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("anthropic: upstream status %d", resp.StatusCode)
	}

	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("anthropic: parse: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("anthropic: %s: %s", ar.Error.Type, ar.Error.Message)
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	return &Response{
		Text:         text,
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
		CostCents:    estimateCostCents(model, ar.Usage.InputTokens, ar.Usage.OutputTokens),
	}, nil
}
