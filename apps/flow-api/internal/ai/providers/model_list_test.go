package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// listRecorder serves canned bodies and records every request it sees.
type listRecorder struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (r *listRecorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req.Clone(context.Background()))
}

func (r *listRecorder) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reqs)
}

// listServer answers every request with body via respond and records it.
func listServer(t *testing.T, respond func(r *http.Request) string) (*httptest.Server, *listRecorder) {
	t.Helper()
	rec := &listRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respond(r))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func listerForTest(t *testing.T, kind Kind, baseURL string) ModelLister {
	t.Helper()
	l, ok := newProviderForTest(t, kind, baseURL).(ModelLister)
	if !ok {
		t.Fatalf("%s provider does not implement ModelLister", kind)
	}
	return l
}

// listBaseURL points each kind at srv the way its generation call would be.
func listBaseURL(kind Kind, srvURL string) string {
	switch kind {
	case KindAnthropic:
		return srvURL + "/v1/messages"
	case KindOpenAICompat:
		return srvURL + "/v1"
	default:
		return srvURL
	}
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestListModelsAnthropic(t *testing.T) {
	t.Parallel()

	srv, rec := listServer(t, func(*http.Request) string {
		return `{"data":[{"id":"claude-sonnet-5-5","type":"model"},{"id":"claude-haiku-4-5","type":"model"}],"has_more":false}`
	})
	ids, err := listerForTest(t, KindAnthropic, srv.URL+"/v1/messages").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	assertIDs(t, ids, []string{"claude-haiku-4-5", "claude-sonnet-5-5"})

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
		t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
	}
	if got := r.URL.Query().Get("limit"); got != "1000" {
		t.Errorf("limit = %q, want 1000", got)
	}
	if got := r.Header.Get("x-api-key"); got != "sealed-key" {
		t.Errorf("x-api-key = %q, want sealed-key", got)
	}
	if got := r.Header.Get("anthropic-version"); got != anthropicAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", got, anthropicAPIVersion)
	}
	if got := r.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}

func TestListModelsAnthropicCustomEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("messages suffix", func(t *testing.T) {
		t.Parallel()
		srv, rec := listServer(t, func(*http.Request) string {
			return `{"data":[{"id":"proxied-model"}]}`
		})
		ids, err := listerForTest(t, KindAnthropic, srv.URL+"/gateway/anthropic/messages").ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		assertIDs(t, ids, []string{"proxied-model"})
		reqs := rec.requests()
		if len(reqs) != 1 || reqs[0].URL.Path != "/gateway/anthropic/models" {
			t.Fatalf("requests = %v, want one to /gateway/anthropic/models", reqs)
		}
		if got := reqs[0].URL.Query().Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
	})

	t.Run("no messages suffix", func(t *testing.T) {
		t.Parallel()
		srv, rec := listServer(t, func(*http.Request) string { return `{"data":[{"id":"x"}]}` })
		ids, err := listerForTest(t, KindAnthropic, srv.URL+"/gateway/chat").ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if ids == nil || len(ids) != 0 {
			t.Fatalf("ids = %#v, want empty non-nil slice", ids)
		}
		if n := len(rec.requests()); n != 0 {
			t.Fatalf("requests = %d, want 0", n)
		}
	})
}

func TestListModelsOpenAICompat(t *testing.T) {
	t.Parallel()

	srv, rec := listServer(t, func(*http.Request) string {
		return `{"object":"list","data":[{"id":"gpt-4o","object":"model"},{"id":"text-embedding-3-small","object":"model"}]}`
	})
	ids, err := listerForTest(t, KindOpenAICompat, srv.URL+"/v1/").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// No name-based filtering: embedding models stay in the candidates.
	assertIDs(t, ids, []string{"gpt-4o", "text-embedding-3-small"})

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if r := reqs[0]; r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
		t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer sealed-key" {
		t.Errorf("Authorization = %q, want Bearer sealed-key", got)
	}
}

func TestListModelsGoogle(t *testing.T) {
	t.Parallel()

	srv, rec := listServer(t, func(*http.Request) string {
		return `{"models":[
			{"name":"models/gemini-b","supportedGenerationMethods":["generateContent","countTokens"]},
			{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]},
			{"name":"models/gemini-a","supportedGenerationMethods":["generateContent"]},
			{"name":"models/aqa"}
		]}`
	})
	ids, err := listerForTest(t, KindGoogle, srv.URL+"/v1beta/models").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	assertIDs(t, ids, []string{"gemini-a", "gemini-b"})

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" {
		t.Errorf("request = %s %s, want GET /v1beta/models", r.Method, r.URL.Path)
	}
	q := r.URL.Query()
	if got := q.Get("pageSize"); got != "1000" {
		t.Errorf("pageSize = %q, want 1000", got)
	}
	if q.Has("key") {
		t.Errorf("API key leaked into the query: %q", r.URL.RawQuery)
	}
	if got := r.Header.Get(googleAPIKeyHeader); got != "sealed-key" {
		t.Errorf("%s = %q, want sealed-key", googleAPIKeyHeader, got)
	}
}

func TestListModelsGooglePagingCap(t *testing.T) {
	t.Parallel()

	page := func(from, to int, next string) string {
		type model struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		}
		models := make([]model, 0, to-from)
		for i := from; i < to; i++ {
			models = append(models, model{Name: fmt.Sprintf("models/m-%04d", i), SupportedGenerationMethods: []string{"generateContent"}})
		}
		raw, _ := json.Marshal(map[string]any{"models": models, "nextPageToken": next})
		return string(raw)
	}
	srv, rec := listServer(t, func(r *http.Request) string {
		switch r.URL.Query().Get("pageToken") {
		case "":
			return page(0, 600, "page-2")
		case "page-2":
			return page(600, 1001, "page-3")
		default:
			return page(1001, 1002, "")
		}
	})
	ids, err := listerForTest(t, KindGoogle, srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(ids) != maxListedModels {
		t.Fatalf("len(ids) = %d, want %d", len(ids), maxListedModels)
	}
	if ids[0] != "m-0000" || ids[maxListedModels-1] != "m-0999" {
		t.Errorf("ids span %q..%q, want m-0000..m-0999", ids[0], ids[maxListedModels-1])
	}

	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (no third page)", len(reqs))
	}
	for i, r := range reqs {
		if got := r.URL.Query().Get("pageSize"); got != "1000" {
			t.Errorf("request %d pageSize = %q, want 1000", i, got)
		}
	}
	if got := reqs[1].URL.Query().Get("pageToken"); got != "page-2" {
		t.Errorf("second request pageToken = %q, want page-2", got)
	}
}

func TestListModelsOllama(t *testing.T) {
	t.Parallel()

	srv, rec := listServer(t, func(*http.Request) string {
		return `{"models":[{"name":"qwen2.5:7b","model":"qwen2.5:7b"},{"name":"llama3.2:latest","model":"llama3.2:latest"}]}`
	})
	ids, err := listerForTest(t, KindOllama, srv.URL+"/").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	assertIDs(t, ids, []string{"llama3.2:latest", "qwen2.5:7b"})

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/api/tags" {
		t.Errorf("request = %s %s, want GET /api/tags", r.Method, r.URL.Path)
	}
	for _, h := range []string{"Authorization", "x-api-key", googleAPIKeyHeader} {
		if got := r.Header.Get(h); got != "" {
			t.Errorf("%s = %q, want none", h, got)
		}
	}
}

func TestListModelsAuthRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUpstreamAuthRejected},
		{http.StatusForbidden, ErrUpstreamAuthRejected},
		{http.StatusTooManyRequests, ErrUpstreamRateLimited},
		{http.StatusNotFound, ErrUpstreamRequestRejected},
	}
	for _, kind := range classifiedKinds {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/%d", kind, tc.status), func(t *testing.T) {
				t.Parallel()
				srv := statusServer(t, tc.status, "", `{"error":{"message":"denied"}}`)
				// A single attempt keeps the 429 case from sleeping through retries.
				ctx := WithSingleAttempt(context.Background())
				ids, err := listerForTest(t, kind, listBaseURL(kind, srv.URL)).ListModels(ctx)
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				if ids != nil {
					t.Errorf("ids = %v, want nil on error", ids)
				}
				if strings.Contains(err.Error(), "sealed-key") {
					t.Errorf("error leaks the key: %v", err)
				}
			})
		}
	}
}

func TestListModelsInvalidJSON(t *testing.T) {
	t.Parallel()

	for _, kind := range classifiedKinds {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			srv := statusServer(t, http.StatusOK, "", "<html>not json</html>")
			_, err := listerForTest(t, kind, listBaseURL(kind, srv.URL)).ListModels(context.Background())
			if !errors.Is(err, ErrResponseInvalidJSON) {
				t.Fatalf("err = %v, want %v", err, ErrResponseInvalidJSON)
			}
		})
	}
}

func TestListModelsDedupSort(t *testing.T) {
	t.Parallel()

	t.Run("upstream", func(t *testing.T) {
		t.Parallel()
		srv, _ := listServer(t, func(*http.Request) string {
			return `{"data":[{"id":"b"},{"id":"a"},{"id":"b"},{"id":""},{"id":"c"},{"id":"a"}]}`
		})
		ids, err := listerForTest(t, KindOpenAICompat, srv.URL).ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		assertIDs(t, ids, []string{"a", "b", "c"})
	})

	t.Run("cap", func(t *testing.T) {
		t.Parallel()
		in := make([]string, 0, 1500)
		for i := 1499; i >= 0; i-- {
			in = append(in, fmt.Sprintf("m-%04d", i), fmt.Sprintf("m-%04d", i))
		}
		got := normalizeModelIDs(in)
		if len(got) != maxListedModels {
			t.Fatalf("len = %d, want %d", len(got), maxListedModels)
		}
		if !slices.IsSorted(got) || got[0] != "m-0000" || got[maxListedModels-1] != "m-0999" {
			t.Errorf("got %q..%q (sorted=%v), want m-0000..m-0999 ascending", got[0], got[maxListedModels-1], slices.IsSorted(got))
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if got := normalizeModelIDs(nil); got == nil || len(got) != 0 {
			t.Errorf("normalizeModelIDs(nil) = %#v, want empty non-nil slice", got)
		}
	})
}
