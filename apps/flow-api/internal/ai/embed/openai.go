package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// Decryptor is the narrow contract used by OpenAIProvider to unseal the
// stored API key ciphertext. In production this is satisfied by
// *crypto.Cipher; tests may inject a fake.
type Decryptor interface {
	Decrypt(blob []byte) ([]byte, error)
}

// identityDecryptor is a fallback Decryptor that returns a copy of the
// input bytes. Used when no real cipher is available at startup so the
// provider still follows the decrypt-use-zero lifecycle.
type identityDecryptor struct{}

func (identityDecryptor) Decrypt(blob []byte) ([]byte, error) {
	out := make([]byte, len(blob))
	copy(out, blob)
	return out, nil
}

const (
	defaultOpenAIEmbedURL   = "https://api.openai.com/v1/embeddings"
	defaultOpenAIEmbedModel = "text-embedding-3-small"
	openAIEmbedTimeout      = 30 * time.Second
)

// OpenAIProvider produces embeddings using the OpenAI Embeddings API.
// It supports text-embedding-3-small (768 dims) and any endpoint that
// speaks the same schema (Azure OpenAI, LiteLLM, vLLM, etc.).
//
// The API key is stored as ciphertext and decrypted per-call via a
// Decryptor. The plaintext is zeroed immediately after use, matching
// the pattern used by the LLM providers in internal/ai/providers.
type OpenAIProvider struct {
	keyCiphertext []byte
	dec           Decryptor
	model         string
	dim           int
	url           string
	client        *http.Client
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
// Embeddings API. keyCiphertext is the encrypted API key blob; dec is
// used to decrypt it on every call. The plaintext is zeroed immediately
// after each use. When dec is nil an identity decryptor is used (the
// blob is treated as plaintext — useful when no cipher is available at
// startup, but the decrypt-use-zero lifecycle still applies).
func NewOpenAIProvider(keyCiphertext []byte, dec Decryptor, opts ...OpenAIOption) *OpenAIProvider {
	if dec == nil {
		dec = identityDecryptor{}
	}
	p := &OpenAIProvider{
		keyCiphertext: keyCiphertext,
		dec:           dec,
		model:         defaultOpenAIEmbedModel,
		dim:           Dim,
		url:           defaultOpenAIEmbedURL,
		client:        &http.Client{Timeout: openAIEmbedTimeout},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model implements Provider.
func (p *OpenAIProvider) Model() string { return p.model }

// ProviderName returns the metrics/audit provider label for embedding calls.
func (p *OpenAIProvider) ProviderName() string { return "openai" }

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

	plain, err := p.dec.Decrypt(p.keyCiphertext)
	if err != nil {
		return nil, fmt.Errorf("openai embed: decrypt key: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+string(plain))
	zero(plain)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai embed: do: %w", classifyEmbedTransportError(ctx, err))
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openai embed: upstream status %d: %w", resp.StatusCode, classifyEmbedHTTPStatus(resp.StatusCode))
	}

	var or openAIEmbedResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("openai embed: parse: %w", providers.ErrResponseInvalidJSON)
	}
	if or.Error != nil {
		return nil, fmt.Errorf("openai embed: upstream error type %q: %w", or.Error.Type, providers.ErrUpstreamRequestRejected)
	}
	if len(or.Data) == 0 || len(or.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai embed: empty embedding in response: %w", providers.ErrResponseSchemaMismatch)
	}
	return or.Data[0].Embedding, nil
}

func classifyEmbedTransportError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return providers.ErrUpstreamTimeout
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return providers.ErrUpstreamTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return providers.ErrUpstreamTimeout
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return providers.ErrUpstreamTimeout
	}
	return providers.ErrUpstreamUnreachable
}

func classifyEmbedHTTPStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return providers.ErrUpstreamAuthRejected
	case http.StatusTooManyRequests:
		return providers.ErrUpstreamRateLimited
	default:
		return providers.ErrUpstreamRequestRejected
	}
}

// zero overwrites b with zero bytes. Used to scrub plaintext API keys
// after they have been written to an outbound Authorization header.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
