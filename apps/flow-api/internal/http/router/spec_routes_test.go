// Package router spec-surface checks.
//
// The document, the reference UI and the JSON Schema of a body are the
// three things flow-api publishes about itself, and all three answer
// callers who have no credentials. What decides which of them exist is
// this package, not huma's config: a config path is mounted once per
// sub-API onto one mux, so a route left to the config is registered
// several times over and answered by whichever group was constructed
// last. The checks here pin both halves — the paths that are not offered
// at all, and the paths that are, serving the merged document rather than
// one group's slice of it.
package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// configDerivedSpecPaths are the document and docs-UI routes huma's
// DefaultConfig mounts on every API built from it. flow-api builds one API
// per chi group, so each of these was mounted once per group on the same
// mux and served the document of whichever group ran last. None of them is
// part of the published surface: the document is offered at /openapi.json
// and the reference UI at /api-reference, both mounted once.
var configDerivedSpecPaths = []string{
	"/docs",
	"/openapi.yaml",
	"/openapi-3.0.json",
	"/openapi-3.0.yaml",
}

// TestConfigDerivedSpecRoutesAreNotMounted asserts the four paths are not
// routable at all.
//
// 404 is the assertion rather than "not 200": a route that still exists
// but refuses callers is a route someone can be given access to later, and
// the point is that nothing is mounted there. The route-tree walk is
// checked alongside the live request because the two can disagree — a
// handler mounted outside chi would answer a request the tree does not
// know about.
func TestConfigDerivedSpecRoutesAreNotMounted(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	inTree := map[string]bool{}
	for _, rr := range res.RoutableOps {
		inTree[routeKey(rr.Method, rr.Path)] = true
	}

	for _, p := range configDerivedSpecPaths {
		if inTree[routeKey(http.MethodGet, p)] {
			t.Errorf("GET %s is in the chi route tree; it is mounted once per sub-API and answered by whichever group was built last, so it must not be mounted at all", p)
		}

		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d, want 404 — the path is meant to be absent, not merely closed", p, rec.Code)
		}
	}
}

// decodeSpec reads /openapi.json off the assembled handler.
func decodeSpec(t *testing.T, res Result) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	res.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json answered %d, want 200", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("GET /openapi.json did not return JSON: %v", err)
	}
	return doc
}

// TestOpenAPIJSONServesTheMergedDocument asserts the surviving document
// route describes every group, not the one whose registration happened to
// land last on the mux.
//
// Each builder's operations are asserted separately because that is the
// failure being ruled out: the sub-APIs each carry their own document, and
// a route serving one of them would still return a valid, plausible
// OpenAPI document — just one missing every path the other groups
// registered. Hidden operations are excluded because huma keeps them out
// of the document deliberately.
func TestOpenAPIJSONServesTheMergedDocument(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	doc := decodeSpec(t, res)

	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("/openapi.json carries no paths object; every assertion below would be vacuous")
	}

	for _, builder := range []struct {
		name string
		ops  []OperationRef
	}{
		{"buildAuthenticatedAPI", res.AuthenticatedOps},
		{"buildPublicShareAPI", res.PublicOps},
	} {
		if len(builder.ops) == 0 {
			t.Fatalf("%s registered no operations; its half of this check would assert nothing", builder.name)
		}
		var missing []string
		for _, op := range builder.ops {
			if op.Hidden {
				continue
			}
			if _, ok := paths[op.Path]; !ok {
				missing = append(missing, routeKey(op.Method, op.Path))
			}
		}
		sort.Strings(missing)
		for _, key := range missing {
			t.Errorf("%s is registered by %s but /openapi.json does not describe it — the route is serving one sub-API's document instead of the merged one", key, builder.name)
		}
	}

	// The last group to register is the one a first-wins regression drops,
	// so it is named rather than left to the loop above: a document assembled
	// from apis[0] alone would still satisfy a check that only looked at the
	// authenticated surface.
	last := res.PublicOps[len(res.PublicOps)-1]
	if _, ok := paths[last.Path]; !ok {
		t.Errorf("%s is the last path any builder registered and /openapi.json omits it", last.Path)
	}
}

// TestAPIReferenceRenders asserts the reference UI is still served and
// still points at the document route that survived.
func TestAPIReferenceRenders(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	req := httptest.NewRequest(http.MethodGet, "/api-reference", nil)
	rec := httptest.NewRecorder()
	res.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api-reference answered %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /api-reference returned Content-Type %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-url="/openapi.json"`) {
		t.Error("the reference UI does not point at /openapi.json, so it renders nothing or renders a document from somewhere else")
	}
}

// TestSchemaURLInAResponseBodyResolves follows the self-description URL a
// real response carries and asserts the route it names answers with that
// body's schema.
//
// This is why /schemas/{schema} is mounted at all. huma's link transformer
// stamps a $schema field into every object response and a describedBy Link
// header beside it, and both name a path under /schemas. The URL is built
// from the config's SchemasPath whether or not anything is mounted there,
// so the route is not optional decoration — dropping it would leave every
// response advertising an address that answers nothing.
func TestSchemaURLInAResponseBodyResolves(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	// /health is the one endpoint that returns a real body without a
	// database or a credential, so its response is a genuine sample of what
	// the transformer emits rather than a document reading.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	res.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health answered %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /health did not return JSON: %v", err)
	}
	raw, ok := body["$schema"].(string)
	if !ok || raw == "" {
		t.Fatal("GET /health carries no $schema field; the transformer that makes /schemas necessary is not running, so this check proves nothing about it")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the $schema URL %q does not parse: %v", raw, err)
	}
	if !strings.HasPrefix(parsed.Path, schemasRoutePath+"/") {
		t.Fatalf("the $schema URL %q does not point under %s, so the route below is not the one it names", raw, schemasRoutePath)
	}

	schema := fetchSchema(t, res, parsed.Path)
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Fatalf("%s returned no properties; the response advertises a schema the server cannot produce", parsed.Path)
	}
	if _, ok := props["status"]; !ok {
		t.Errorf("%s does not describe the field the body actually returned; it is answering with some other type's schema", parsed.Path)
	}

	// The header carries the same address in relative form. A response whose
	// two self-descriptions disagree sends half its callers somewhere else.
	link := rec.Header().Get("Link")
	if !strings.Contains(link, "<"+parsed.Path+">") {
		t.Errorf("the describedBy Link header %q does not name %s", link, parsed.Path)
	}
}

// TestEverySchemaURLTheDocumentAdvertisesResolves walks the published
// document and follows every self-description URL it declares.
//
// Each sub-API keeps its own schema registry holding only the types its
// own group registered, so a /schemas route taken from one of them answers
// for that group and returns a JSON null for every other group's types —
// while the document, which is merged, advertises all of them. Checking
// one URL cannot see that: it either lands in the answering group or it
// does not. Following all of them is what distinguishes a route serving
// the merged registry from one serving a slice of it.
func TestEverySchemaURLTheDocumentAdvertisesResolves(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	doc := decodeSpec(t, res)

	advertised := advertisedSchemaPaths(t, doc)
	if len(advertised) == 0 {
		t.Fatal("the published document advertises no $schema URL; this check would walk nothing")
	}

	for _, p := range advertised {
		schema := fetchSchema(t, res, p)
		if schema == nil {
			t.Errorf("%s is advertised by the published document but resolves to a JSON null — the route is answering from one group's registry rather than the merged one", p)
		}
	}
}

// advertisedSchemaPaths collects the distinct /schemas paths the document's
// own component schemas name in their $schema examples, sorted so a
// failure names the same one on every run.
func advertisedSchemaPaths(t *testing.T, doc map[string]any) []string {
	t.Helper()
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatal("the published document declares no component schemas")
	}

	seen := map[string]bool{}
	for _, v := range schemas {
		schema, ok := v.(map[string]any)
		if !ok {
			continue
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		self, ok := props["$schema"].(map[string]any)
		if !ok {
			continue
		}
		examples, ok := self["examples"].([]any)
		if !ok || len(examples) == 0 {
			continue
		}
		raw, ok := examples[0].(string)
		if !ok {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || !strings.HasPrefix(parsed.Path, schemasRoutePath+"/") {
			continue
		}
		seen[parsed.Path] = true
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// fetchSchema GETs a /schemas path and decodes it. A non-200 fails the
// calling check; a JSON null decodes to a nil map, which the caller reads
// as "the route did not have this type".
func fetchSchema(t *testing.T, res Result, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	res.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET %s answered %d, want 200 — a URL the API hands out has to resolve", path, rec.Code)
		return map[string]any{"unreachable": true}
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Errorf("GET %s did not return JSON: %v", path, err)
		return map[string]any{"unreachable": true}
	}
	return out
}

// TestNestedSchemaRefsPointAtTheRouteThatServesThem asserts a schema served
// under /schemas has its in-document references rewritten into addresses on
// the same route.
//
// A component served on its own is not accompanied by the document its
// "#/components/schemas/..." references resolve against, so a caller that
// followed a $schema URL and then followed a nested reference would be
// pointed back into a file it does not have.
func TestNestedSchemaRefsPointAtTheRouteThatServesThem(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	doc := decodeSpec(t, res)

	var withRef string
	for _, p := range advertisedSchemaPaths(t, doc) {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"$ref"`) {
			withRef = rec.Body.String()
			if strings.Contains(withRef, schemaRefPrefix) {
				t.Errorf("GET %s serves a schema still referring to %s, which resolves only inside the full document", p, schemaRefPrefix)
			}
			break
		}
	}
	if withRef == "" {
		t.Skip("no advertised schema carries a nested $ref, so there is nothing here to rewrite")
	}
	if !strings.Contains(withRef, `"$ref":"`+schemasRoutePath+"/") {
		t.Errorf("a served schema's nested $ref does not address %s/{schema}: %s", schemasRoutePath, truncate(withRef, 300))
	}
}

// truncate keeps a failure message readable when the value is a whole
// serialized schema.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
