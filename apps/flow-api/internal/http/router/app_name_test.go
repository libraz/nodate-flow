package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fetch(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	return rec.Body.String()
}

func specTitle(t *testing.T, h http.Handler) string {
	t.Helper()
	var doc struct {
		Info struct {
			Title string `json:"title"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(fetch(t, h, "/openapi.json")), &doc); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	return doc.Info.Title
}

func TestOpenAPITitleUsesAppName(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.AppName = "Acme"
	h := BuildResult(deps).Handler
	if got := specTitle(t, h); got != "Acme" {
		t.Errorf("info.title = %q, want Acme", got)
	}
	if page := fetch(t, h, "/api-reference"); !strings.Contains(page, "<title>Acme API Reference</title>") {
		t.Errorf("scalar title not Acme: %s", page)
	}

	deps = stubDeps(t)
	deps.AppName = "A<b>"
	page := fetch(t, BuildResult(deps).Handler, "/api-reference")
	if !strings.Contains(page, "<title>A&lt;b&gt; API Reference</title>") {
		t.Errorf("scalar title not escaped: %s", page)
	}
}

func TestOpenAPITitleDefaultsWhenUnset(t *testing.T) {
	t.Parallel()
	h := BuildResult(stubDeps(t)).Handler
	if got := specTitle(t, h); got != "nodate-flow" {
		t.Errorf("info.title = %q, want nodate-flow", got)
	}
	if page := fetch(t, h, "/api-reference"); !strings.Contains(page, "<title>nodate-flow API Reference</title>") {
		t.Errorf("scalar title not default: %s", page)
	}
}
