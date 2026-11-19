package embed

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

type copyingDecryptor struct{}

func (copyingDecryptor) Decrypt(blob []byte) ([]byte, error) {
	return bytes.Clone(blob), nil
}

func TestOpenAIProviderClassifiesHTTPErrorWithoutLeakingBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"secret upstream detail"}}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewOpenAIProvider([]byte("key"), copyingDecryptor{}, WithOpenAIBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	_, err = p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed succeeded, want error")
	}
	if !errors.Is(err, providers.ErrUpstreamAuthRejected) {
		t.Fatalf("error = %v, want ErrUpstreamAuthRejected", err)
	}
	if strings.Contains(err.Error(), "secret upstream detail") {
		t.Fatalf("error leaked upstream body: %v", err)
	}
}

func TestOpenAIProviderClassifiesInvalidJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewOpenAIProvider([]byte("key"), copyingDecryptor{}, WithOpenAIBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	_, err = p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed succeeded, want error")
	}
	if !errors.Is(err, providers.ErrResponseInvalidJSON) {
		t.Fatalf("error = %v, want ErrResponseInvalidJSON", err)
	}
}

func TestOpenAIProviderDoesNotExposeUpstreamErrorFields(t *testing.T) {
	t.Parallel()
	const marker = "upstream-echoed-content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"` + marker + `","type":"` + marker + `"}}`))
	}))
	t.Cleanup(srv.Close)

	p, err := NewOpenAIProvider([]byte("key"), copyingDecryptor{}, WithOpenAIBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	_, err = p.Embed(context.Background(), "private task text")
	if err == nil {
		t.Fatal("Embed succeeded, want error")
	}
	if !errors.Is(err, providers.ErrUpstreamRequestRejected) {
		t.Fatalf("error = %v, want ErrUpstreamRequestRejected", err)
	}
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "private task text") {
		t.Fatalf("error exposed upstream or task content: %v", err)
	}
}

func TestValidateOpenAIBaseURLRequiresHTTPSOutsideDevelopment(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://example.com/v1",
		"https://user:password@example.com/v1",
		"https://example.com/v1?key=secret",
		"https://example.com/v1#secret",
		"/v1",
	} {
		if err := ValidateOpenAIBaseURL(raw, false); err == nil {
			t.Errorf("ValidateOpenAIBaseURL(%q, false) succeeded", raw)
		}
	}
	if err := ValidateOpenAIBaseURL("http://localhost:1234/v1", true); err != nil {
		t.Fatalf("development HTTP endpoint rejected: %v", err)
	}
	if err := ValidateOpenAIBaseURL("https://api.example.com/v1", false); err != nil {
		t.Fatalf("HTTPS endpoint rejected: %v", err)
	}
}

func TestOpenAIProviderPublicEndpointOmitsPath(t *testing.T) {
	t.Parallel()
	p, err := NewOpenAIProvider([]byte("key"), copyingDecryptor{}, WithOpenAIBaseURL("https://api.example.com/private-token/v1"))
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	if got, want := p.PublicEndpoint(), "https://api.example.com"; got != want {
		t.Fatalf("PublicEndpoint() = %q, want %q", got, want)
	}
}
