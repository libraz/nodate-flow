package embed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

func TestOpenAIProviderClassifiesHTTPErrorWithoutLeakingBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"secret upstream detail"}}`))
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider([]byte("key"), nil, WithOpenAIBaseURL(srv.URL))
	_, err := p.Embed(context.Background(), "hello")
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

	p := NewOpenAIProvider([]byte("key"), nil, WithOpenAIBaseURL(srv.URL))
	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("Embed succeeded, want error")
	}
	if !errors.Is(err, providers.ErrResponseInvalidJSON) {
		t.Fatalf("error = %v, want ErrResponseInvalidJSON", err)
	}
}
