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
	defaultOllamaBaseURL = "http://localhost:11434"
	ollamaDefaultModel   = "llama3.2"
)

// ollamaProvider talks to a local Ollama daemon. Ollama needs no auth, so
// no decryption path runs here even though the Provider interface allows
// it.
type ollamaProvider struct {
	cfg Config
}

func (p *ollamaProvider) Name() string { return p.cfg.Name }
func (p *ollamaProvider) Kind() Kind   { return KindOllama }

type ollamaReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream bool   `json:"stream"`
}

type ollamaResp struct {
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (p *ollamaProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = chooseBaseURL(p.cfg.DefaultModel, ollamaDefaultModel)
	}
	body, err := json.Marshal(ollamaReq{
		Model:  model,
		Prompt: req.Prompt,
		System: req.System,
		Stream: false,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal: %w", err)
	}
	base := chooseBaseURL(p.cfg.BaseURL, defaultOllamaBaseURL)
	url := strings.TrimRight(base, "/") + "/api/generate"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doLimited(ctx, DestOllama, httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("ollama: read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ollama: upstream status %d", resp.StatusCode)
	}

	var or ollamaResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("ollama: parse: %w", err)
	}
	if or.Error != "" {
		return nil, fmt.Errorf("ollama: %s", or.Error)
	}
	return &Response{
		Text:         or.Response,
		InputTokens:  or.PromptEvalCount,
		OutputTokens: or.EvalCount,
	}, nil
}
