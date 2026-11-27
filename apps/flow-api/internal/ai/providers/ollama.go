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
func (p *ollamaProvider) Model() string {
	return chooseBaseURL(p.cfg.DefaultModel, ollamaDefaultModel)
}

type ollamaReq struct {
	Model  string          `json:"model"`
	Prompt string          `json:"prompt"`
	System string          `json:"system,omitempty"`
	Stream bool            `json:"stream"`
	Format json.RawMessage `json:"format,omitempty"`
	// Options is Ollama's sampling block. Omitted entirely when the
	// caller chose neither knob.
	Options *ollamaOptions `json:"options,omitempty"`
}

type ollamaOptions struct {
	// NumPredict is Ollama's name for the output token cap.
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

// ollamaOptionsFor returns the sampling block for req, or nil when the
// caller chose neither knob.
func ollamaOptionsFor(req Request) *ollamaOptions {
	if req.MaxTokens <= 0 && req.Temperature == nil {
		return nil
	}
	opts := &ollamaOptions{Temperature: req.Temperature}
	if req.MaxTokens > 0 {
		opts.NumPredict = req.MaxTokens
	}
	return opts
}

type ollamaResp struct {
	Response        string `json:"response"`
	Done            *bool  `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (p *ollamaProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.Model()
	}
	outputMode, nativeSchema := nativeSchemaFor(KindOllama, model, chooseBaseURL(p.cfg.BaseURL, defaultOllamaBaseURL), req.Output)
	body, err := json.Marshal(ollamaReq{
		Model:   model,
		Prompt:  req.Prompt,
		System:  req.System,
		Stream:  false,
		Format:  nativeSchema,
		Options: ollamaOptionsFor(req),
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

	var or ollamaResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, &UpstreamError{Sentinel: ErrResponseInvalidJSON, Status: resp.StatusCode}
	}
	if or.Error != "" {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: resp.StatusCode}
	}
	termination, stopReason := classifyOllamaCompletion(or.Done, or.DoneReason)
	// Ollama runs on hardware the operator already owns, so the call has
	// no per-token price to report. Saying so explicitly — model named,
	// cost zero — is the whole point: a response that named neither left
	// the cost to be guessed downstream from the model name, and since
	// local model names are absent from the price table the guess was the
	// most expensive rate in it.
	return &Response{
		Model:        model,
		Text:         or.Response,
		Termination:  termination,
		StopReason:   stopReason,
		OutputMode:   outputMode,
		InputTokens:  or.PromptEvalCount,
		OutputTokens: or.EvalCount,
		CostMicros:   0,
		CostCents:    0,
	}, nil
}

type ollamaTags struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	Error string `json:"error,omitempty"`
}

// ListModels implements [ModelLister] with GET {base}/api/tags, the
// locally pulled models. Like Complete it sends no credentials.
func (p *ollamaProvider) ListModels(ctx context.Context) ([]string, error) {
	base := chooseBaseURL(p.cfg.BaseURL, defaultOllamaBaseURL)
	listURL := strings.TrimRight(base, "/") + "/api/tags"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}

	var tags ollamaTags
	status, err := fetchModelList(ctx, DestOllama, httpReq, &tags)
	if err != nil {
		return nil, err
	}
	if tags.Error != "" {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: status}
	}
	ids := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		ids = append(ids, m.Name)
	}
	return normalizeModelIDs(ids), nil
}

func classifyOllamaCompletion(done *bool, reason string) (Termination, string) {
	if done == nil || !*done {
		return TerminationUnknown, boundedStopReason(reason)
	}
	bounded := boundedStopReason(reason)
	switch reason {
	case "stop":
		return TerminationComplete, bounded
	case "length":
		return TerminationIncomplete, bounded
	default:
		return TerminationUnknown, bounded
	}
}
