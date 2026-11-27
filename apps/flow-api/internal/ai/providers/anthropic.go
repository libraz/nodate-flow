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
	anthropicEndpoint    = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	anthropicDefaultMdl  = "claude-sonnet-5-5"
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
	return chooseBaseURL(p.cfg.DefaultModel, OfficialDefaultModel(KindAnthropic))
}

type anthropicReq struct {
	Model        string                 `json:"model"`
	MaxTokens    int                    `json:"max_tokens"`
	System       string                 `json:"system,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	// Temperature is omitted when the caller did not choose one, leaving
	// the model's own default in force.
	Temperature *float64           `json:"temperature,omitempty"`
	Thinking    *anthropicThinking `json:"thinking,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

type anthropicOutputConfig struct {
	Format anthropicOutputFormat `json:"format"`
}

type anthropicOutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content    []anthropicRespBlock `json:"content"`
	StopReason *string              `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicRespBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
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
	outputMode, outputConfig := anthropicOutputConfigFor(model, chooseBaseURL(p.endpoint, anthropicEndpoint), req.Output)
	temperature := req.Temperature
	var thinking *anthropicThinking
	if model == "claude-sonnet-5-5" {
		// Sonnet 5.5 rejects the legacy temperature field. Its migration
		// contract also requires the between-tools thinking mode so reasoning
		// does not consume the caller's bounded output budget.
		temperature = nil
		thinking = &anthropicThinking{Type: "between_tools"}
	}
	body, err := json.Marshal(anthropicReq{
		Model:        model,
		MaxTokens:    maxTok,
		System:       req.System,
		Messages:     []anthropicMessage{{Role: "user", Content: req.Prompt}},
		Temperature:  temperature,
		Thinking:     thinking,
		OutputConfig: outputConfig,
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
	if len(ar.Content) == 0 && ar.StopReason == nil && ar.Usage.InputTokens == 0 && ar.Usage.OutputTokens == 0 {
		return nil, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	termination, stopReason := classifyAnthropicCompletion(ar.StopReason, ar.Content)
	costMicros := EstimateCostMicrosUSD(model, ar.Usage.InputTokens, ar.Usage.OutputTokens)
	return &Response{
		Model:        model,
		Text:         text,
		Termination:  termination,
		StopReason:   stopReason,
		OutputMode:   outputMode,
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}, nil
}

type anthropicModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Type string `json:"type"`
	} `json:"error,omitempty"`
}

// ListModels implements [ModelLister] against the Models API, which sits
// beside the configured Messages endpoint. An endpoint not ending in
// /messages has no known models sibling, so it yields no candidates.
func (p *anthropicProvider) ListModels(ctx context.Context) ([]string, error) {
	endpoint := chooseBaseURL(p.endpoint, anthropicEndpoint)
	if !strings.HasSuffix(endpoint, "/messages") {
		return []string{}, nil
	}
	listURL := strings.TrimSuffix(endpoint, "/messages") + "/models?limit=1000"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("anthropic: decrypt key: %w: %w", ErrKeyDecryptFailed, err)
	}
	httpReq.Header.Set("x-api-key", string(plain))
	zero(plain)

	var list anthropicModelList
	status, err := fetchModelList(ctx, DestAnthropic, httpReq, &list)
	if err != nil {
		return nil, err
	}
	if list.Error != nil {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: status}
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return normalizeModelIDs(ids), nil
}

func anthropicOutputConfigFor(model, endpoint string, spec *OutputSpec) (OutputMode, *anthropicOutputConfig) {
	mode, nativeSchema := nativeSchemaFor(KindAnthropic, model, endpoint, spec)
	if mode != OutputModeNativeSchema {
		return mode, nil
	}
	return mode, &anthropicOutputConfig{Format: anthropicOutputFormat{Type: "json_schema", Schema: nativeSchema}}
}

func classifyAnthropicCompletion(stopReason *string, blocks []anthropicRespBlock) (Termination, string) {
	for _, block := range blocks {
		if block.Type == "refusal" {
			return TerminationRefused, "refusal"
		}
	}
	if stopReason == nil {
		return TerminationUnknown, ""
	}
	reason := boundedStopReason(*stopReason)
	switch *stopReason {
	case "end_turn":
		return TerminationComplete, reason
	case "max_tokens", "model_context_window_exceeded", "pause_turn", "tool_use":
		return TerminationIncomplete, reason
	case "refusal":
		return TerminationRefused, reason
	default:
		// No stop sequences are sent by this adapter, so stop_sequence (and
		// any future value) is not proof that a full answer was generated.
		return TerminationUnknown, reason
	}
}
