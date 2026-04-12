package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenAIEmbedURL   = "https://api.openai.com/v1/embeddings"
	defaultOpenAIEmbedModel = "text-embedding-3-small"
	openAIEmbedTimeout      = 30 * time.Second
)

// OpenAIProvider produces embeddings using the OpenAI Embeddings API.
// It supports text-embedding-3-small (768 dims) and any endpoint that
// speaks the same schema (Azure OpenAI, LiteLLM, vLLM, etc.).
type OpenAIProvider struct {
	apiKey string
	model  string
	dim    int
	url    string
	client *http.Client
}

// OpenAIOption configures an OpenAIProvider.
type OpenAIOption func(*OpenAIProvider)

// WithOpenAIModel overrides the default embedding model.
func WithOpenAIModel(m string) OpenAIOption {
	return func(p *OpenAIProvider) { p.model = m }
}

// WithOpenAIDim overrides the requested embedding dimension.
func WithOpenAIDim(d int) OpenAIOption {
	return func(p *OpenAIProvider) { p.dim = d }
}

// WithOpenAIBaseURL overrides the API base URL for compatible endpoints.
func WithOpenAIBaseURL(u string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.url = strings.TrimRight(u, "/") + "/embeddings"
	}
}

// NewOpenAIProvider creates an embedding provider backed by the OpenAI
// Embeddings API. apiKey is the plaintext API key. The caller is
// responsible for decrypting the key before passing it here.
func NewOpenAIProvider(apiKey string, opts ...OpenAIOption) *OpenAIProvider {
	p := &OpenAIProvider{
		apiKey: apiKey,
		model:  defaultOpenAIEmbedModel,
		dim:    Dim,
		url:    defaultOpenAIEmbedURL,
		client: &http.Client{Timeout: openAIEmbedTimeout},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model implements Provider.
func (p *OpenAIProvider) Model() string { return p.model }

type openAIEmbedReq struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type openAIEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Embed implements Provider. It calls the OpenAI embeddings endpoint and
// returns the raw vector. The Client normalizes it afterwards.
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody := openAIEmbedReq{
		Model: p.model,
		Input: text,
	}
	// text-embedding-3-* models support the dimensions parameter.
	if strings.Contains(p.model, "embedding-3") {
		reqBody.Dimensions = p.dim
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai embed: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embed: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai embed: do: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openai embed: upstream status %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var or openAIEmbedResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("openai embed: parse: %w", err)
	}
	if or.Error != nil {
		return nil, fmt.Errorf("openai embed: %s: %s", or.Error.Type, or.Error.Message)
	}
	if len(or.Data) == 0 || len(or.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai embed: empty embedding in response")
	}
	return or.Data[0].Embedding, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
