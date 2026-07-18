package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	googleBaseURL      = "https://generativelanguage.googleapis.com/v1beta/models"
	googleDefaultModel = "gemini-1.5-flash-latest"
	// googleAPIKeyHeader is Gemini's supported alternative to the ?key=
	// query parameter. Passing the key in a header keeps it out of the
	// request URL, so it can never leak through a url.Error message,
	// transport log, or proxy access log built from the URL.
	googleAPIKeyHeader = "x-goog-api-key" //#nosec G101 -- HTTP header name, not a credential
)

// ErrInvalidBaseURL is returned by [New] when a provider's configured
// base URL is present but not a parseable absolute http(s) URL. Validating
// at construction turns a malformed ai_providers.base_url into a fast,
// stable failure instead of an opaque transport error mid-call.
var ErrInvalidBaseURL = errors.New("ai/providers: invalid base url")

// validateBaseURL parses raw and rejects anything that is not an absolute
// http or https URL. An empty string is accepted: it means "use the
// provider default endpoint".
func validateBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme must be http or https", ErrInvalidBaseURL)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidBaseURL)
	}
	return nil
}

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

	base := chooseBaseURL(p.baseURL, googleBaseURL)
	endpoint := fmt.Sprintf("%s/%s:generateContent", base, url.PathEscape(model))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	plain, err := p.dec.Decrypt(p.cfg.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("google: decrypt key: %w", err)
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
	if len(gr.Candidates) == 0 {
		return nil, &UpstreamError{Sentinel: ErrResponseSchemaMismatch, Status: resp.StatusCode}
	}
	var text string
	for _, part := range gr.Candidates[0].Content.Parts {
		text += part.Text
	}
	costMicros := EstimateCostMicrosUSD(model, gr.UsageMetadata.PromptTokenCount, gr.UsageMetadata.CandidatesTokenCount)
	return &Response{
		Model:        model,
		Text:         text,
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		CostMicros:   costMicros,
		CostCents:    costMicros / 10_000,
	}, nil
}
