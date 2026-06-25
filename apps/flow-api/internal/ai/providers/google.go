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
	googleDefaultModel = "gemini-1.5-flash-latest"
)

// googleProvider talks to Gemini's generateContent endpoint. The API key
// is passed as a query parameter (?key=...) per Google's spec; we still
// scrub the plaintext immediately after the URL is built.
type googleProvider struct {
	cfg     Config
	dec     Decryptor
	baseURL string
}

func (p *googleProvider) Name() string { return p.cfg.Name }
func (p *googleProvider) Kind() Kind   { return KindGoogle }

type googleReq struct {
	Contents []googleContent `json:"contents"`
}

type googleContent struct {
	Role  string       `json:"role"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text string `json:"text"`
}

type googleResp struct {
	Candidates []struct {
		Content googleContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *googleProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = chooseBaseURL(p.cfg.DefaultModel, googleDefaultModel)
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + prompt
	}
	body, err := json.Marshal(googleReq{
		Contents: []googleContent{{Role: "user", Parts: []googlePart{{Text: prompt}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("google: marshal: %w", err)
	}

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("google: decrypt key: %w", err)
	}
	base := chooseBaseURL(p.baseURL, googleBaseURL)
	endpoint := fmt.Sprintf("%s/%s:generateContent?key=%s", base, url.PathEscape(model), url.QueryEscape(string(plain)))
	zero(plain)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doLimited(ctx, DestGoogle, httpReq)
	if err != nil {
		// classifyTransportError builds its message from a sentinel only,
		// so the API key Google requires in the query string is never
		// surfaced in the returned error.
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
	if len(gr.Candidates) == 0 {
		return nil, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	var text string
	for _, part := range gr.Candidates[0].Content.Parts {
		text += part.Text
	}
	return &Response{
		Text:         text,
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		CostCents:    estimateCostCents(model, gr.UsageMetadata.PromptTokenCount, gr.UsageMetadata.CandidatesTokenCount),
	}, nil
}
