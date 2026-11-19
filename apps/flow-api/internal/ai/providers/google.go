package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	googleBaseURL      = "https://generativelanguage.googleapis.com/v1beta/models"
	googleDefaultModel = "gemini-4-hydrogen"
	// googleAPIKeyHeader is Gemini's supported alternative to the ?key=
	// query parameter. Passing the key in a header keeps it out of the
	// request URL, so it can never leak through a url.Error message,
	// transport log, or proxy access log built from the URL.
	googleAPIKeyHeader = "x-goog-api-key" //#nosec G101 -- HTTP header name, not a credential
)

// googleProvider talks to Gemini's generateContent endpoint. The API key
// is passed via the x-goog-api-key request header (never the URL query),
// and the decrypted plaintext is zeroed immediately after the header is
// set so it is never held beyond the single upstream call.
type googleProvider struct {
	cfg     Config
	dec     Decryptor
	baseURL string
}

func (p *googleProvider) Name() string { return p.cfg.Name }
func (p *googleProvider) Kind() Kind   { return KindGoogle }
func (p *googleProvider) Model() string {
	return chooseBaseURL(p.cfg.DefaultModel, OfficialDefaultModel(KindGoogle))
}

type googleReq struct {
	Contents []googleContent `json:"contents"`
	// GenerationConfig carries the sampling knobs. Gemini spells them
	// differently from the other providers (nested, and maxOutputTokens
	// rather than max_tokens), which is how they came to be dropped here
	// while every sibling provider forwarded them.
	GenerationConfig *googleGenerationConfig `json:"generationConfig,omitempty"`
}

type googleGenerationConfig struct {
	MaxOutputTokens    int                   `json:"maxOutputTokens,omitempty"`
	Temperature        *float64              `json:"temperature,omitempty"`
	ThinkingConfig     *googleThinkingConfig `json:"thinkingConfig,omitempty"`
	ResponseMIMEType   string                `json:"responseMimeType,omitempty"`
	ResponseJSONSchema json.RawMessage       `json:"responseJsonSchema,omitempty"`
}

type googleThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

// generationConfig returns the model's supported generation controls. The
// current Hydrogen API rejects sampling controls, so its path sends only a
// bounded output cap, low thinking, and an optional verified response schema.
func generationConfig(model string, req Request, nativeSchema json.RawMessage) *googleGenerationConfig {
	modernModel := model == googleDefaultModel
	if req.MaxTokens <= 0 && req.Temperature == nil && !modernModel && len(nativeSchema) == 0 {
		return nil
	}
	cfg := &googleGenerationConfig{}
	if modernModel {
		cfg.ThinkingConfig = &googleThinkingConfig{ThinkingLevel: "LOW"}
	} else {
		cfg.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = req.MaxTokens
	}
	if len(nativeSchema) > 0 {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseJSONSchema = append(json.RawMessage(nil), nativeSchema...)
	}
	return cfg
}

type googleContent struct {
	Role  string       `json:"role"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text         string          `json:"text"`
	Thought      bool            `json:"thought"`
	FunctionCall json.RawMessage `json:"functionCall"`
}

type googleResp struct {
	Candidates []struct {
		Content      googleContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *googleProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.Model()
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + prompt
	}
	base := chooseBaseURL(p.baseURL, googleBaseURL)
	outputMode, nativeSchema := nativeSchemaFor(KindGoogle, model, base, req.Output)
	body, err := json.Marshal(googleReq{
		Contents:         []googleContent{{Role: "user", Parts: []googlePart{{Text: prompt}}}},
		GenerationConfig: generationConfig(model, req, nativeSchema),
	})
	if err != nil {
		return nil, fmt.Errorf("google: marshal: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s:generateContent", base, url.PathEscape(model))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("google: decrypt key: %w: %w", ErrKeyDecryptFailed, err)
	}
	// string(plain) copies the bytes into the immutable header value, so
	// zeroing the plaintext slice afterwards drops our only mutable copy;
	// the key never appears in the request URL.
	httpReq.Header.Set(googleAPIKeyHeader, string(plain))
	zero(plain)

	resp, err := doLimited(ctx, DestGoogle, httpReq)
	if err != nil {
		// classifyTransportError builds its message from a sentinel only,
		// so even a transport error whose wrapped url.Error embeds the
		// request URL cannot surface the API key (it rides in the header,
		// not the URL).
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

	var gr googleResp
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, &UpstreamError{Sentinel: ErrResponseInvalidJSON, Status: resp.StatusCode}
	}
	if gr.Error != nil {
		return nil, &UpstreamError{Sentinel: ErrUpstreamRequestRejected, Status: resp.StatusCode}
	}
	outputTokens := gr.UsageMetadata.CandidatesTokenCount + gr.UsageMetadata.ThoughtsTokenCount
	costMicros := EstimateCostMicrosUSD(model, gr.UsageMetadata.PromptTokenCount, outputTokens)
	out := &Response{
		Model:        model,
		OutputMode:   outputMode,
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: outputTokens,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}
	// The exchange was billed even without a candidate, so usage goes back with the error.
	if len(gr.Candidates) == 0 && !googlePromptBlocked(gr.PromptFeedback.BlockReason) {
		return out, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	var text string
	var finishReason string
	var parts []googlePart
	if len(gr.Candidates) > 0 {
		finishReason = gr.Candidates[0].FinishReason
		parts = gr.Candidates[0].Content.Parts
		for _, part := range parts {
			if !part.Thought && len(part.FunctionCall) == 0 && part.Text != "" {
				text += part.Text
			}
		}
	}
	out.Text = text
	out.Termination, out.StopReason = classifyGoogleCompletion(finishReason, gr.PromptFeedback.BlockReason, parts)
	return out, nil
}

func googlePromptBlocked(reason string) bool {
	return reason != "" && reason != "BLOCK_REASON_UNSPECIFIED" && reason != "UNSPECIFIED"
}

func classifyGoogleCompletion(finishReason, blockReason string, parts []googlePart) (Termination, string) {
	if googlePromptBlocked(blockReason) {
		return TerminationRefused, boundedStopReason(blockReason)
	}
	switch finishReason {
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return TerminationRefused, boundedStopReason(finishReason)
	}
	for _, part := range parts {
		if len(part.FunctionCall) > 0 && string(part.FunctionCall) != "null" {
			return TerminationIncomplete, "function_call"
		}
	}
	switch finishReason {
	case "STOP":
		return TerminationComplete, "stop"
	case "MAX_TOKENS":
		return TerminationIncomplete, "max_tokens"
	default:
		return TerminationUnknown, boundedStopReason(finishReason)
	}
}
