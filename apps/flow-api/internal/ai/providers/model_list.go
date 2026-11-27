package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
)

// maxListedModels caps how many model IDs ListModels returns. OpenRouter
// lists about 740 models with every output modality included.
const maxListedModels = 1000

// ModelLister is implemented by providers that can enumerate the model IDs
// their API key can see. It is optional: callers type-assert a Provider and
// treat a provider without it (the mock included) as having no candidates.
//
// The returned IDs are de-duplicated, sorted ascending and capped at
// [maxListedModels]. Upstream failures carry the same sentinels as Complete.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

var (
	_ ModelLister = (*anthropicProvider)(nil)
	_ ModelLister = (*openAIProvider)(nil)
	_ ModelLister = (*googleProvider)(nil)
	_ ModelLister = (*ollamaProvider)(nil)
)

// fetchModelList sends a built list request through the same egress path,
// body cap and status classification as Complete, and decodes the body
// into out. It returns the upstream status for callers that map an in-body
// error object.
func fetchModelList(ctx context.Context, destination string, req *http.Request, out any) (int, error) {
	resp, err := doLimited(ctx, destination, req)
	if err != nil {
		return 0, classifyTransportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, classifyTransportError(ctx, err)
	}
	if uerr := classifyHTTPStatus(resp.StatusCode, resp.Header.Get("Retry-After")); uerr != nil {
		return resp.StatusCode, uerr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, &UpstreamError{Sentinel: ErrResponseInvalidJSON, Status: resp.StatusCode}
	}
	return resp.StatusCode, nil
}

// normalizeModelIDs drops empty and duplicate IDs, sorts ascending and caps
// the result at [maxListedModels]. It never returns nil.
func normalizeModelIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) > maxListedModels {
		out = out[:maxListedModels]
	}
	return out
}
