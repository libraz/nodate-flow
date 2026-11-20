package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/libraz/nodate-flow/packages/go-shared/openapiutil"
)

// scalarHTML is the self-contained HTML page that loads the Scalar API
// reference UI from their CDN. data-url is relative so it resolves to the
// sibling /openapi.json of /api-reference both at the root and under a
// path prefix.
func scalarHTML(appName string) string {
	return fmt.Sprintf(scalarHTMLTemplate, html.EscapeString(appName))
}

const scalarHTMLTemplate = `<!DOCTYPE html>
<html>
<head>
  <title>%s API Reference</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url="openapi.json"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`

// mergeOpenAPI merges the separate huma.API OpenAPI documents into a
// single spec. It runs the same openapiutil.MergeAPIs that cmd/dump-openapi
// runs, at router build time, so the running server can serve the spec
// without a filesystem artifact.
//
// A merge error means two sub-APIs claim one component name for two
// different definitions, so there is no spec that describes both.
// Panicking stops the process at build time, where huma already panics
// for a duplicate operation ID; the alternative is a server that starts
// and serves a document describing one of the two operations wrongly.
func mergeOpenAPI(apis []huma.API) *huma.OpenAPI {
	merged, err := openapiutil.MergeAPIs(apis)
	if err != nil {
		panic(err)
	}
	return merged
}

// marshalOpenAPI renders the merged document as the bytes /openapi.json
// serves.
func marshalOpenAPI(merged *huma.OpenAPI, appName string) []byte {
	buf, err := json.Marshal(merged)
	if err != nil {
		// Should never happen: Huma's OpenAPI types are always
		// serializable. Fall back to a minimal valid document so
		// /openapi.json never 500s.
		title, _ := json.Marshal(appName)
		return []byte(`{"openapi":"3.1.0","info":{"title":` + string(title) + `,"version":"0.0.0"},"paths":{}}`)
	}
	return buf
}

// schemasRoutePath is the prefix the JSON Schema of a request or response
// body is served under. It has to stay equal to the SchemasPath the
// sub-API config carries, because that is the value huma's link
// transformer builds the $schema URL from: a response advertises
// {host}/schemas/{Name}.json, and this is the route that has to answer it.
const schemasRoutePath = "/schemas"

// schemaRefPrefix is how a component reference is spelled inside the
// document. Names run from the end of this prefix to the closing quote.
const schemaRefPrefix = "#/components/schemas/"

// mountSchemasRoute serves each component schema of the merged document
// under /schemas/{schema}.
//
// It is mounted here, from the merged registry, rather than left to the
// per-sub-API route huma's config mounts. Each sub-API has its own schema
// registry holding only the types its own group registered, so that route
// answers for one group's types and returns a JSON null for every other
// group's — including, today, the types named by the $schema URLs those
// same groups stamp into their responses. Which group answers is decided
// by the order the builders ran in.
func mountSchemasRoute(r chi.Router, merged *huma.OpenAPI) {
	var schemas map[string]*huma.Schema
	if merged != nil && merged.Components != nil && merged.Components.Schemas != nil {
		schemas = merged.Components.Schemas.Map()
	}
	r.Get(schemasRoutePath+"/{schema}", func(w http.ResponseWriter, req *http.Request) {
		// The .json suffix is part of the advertised URL rather than of the
		// component name, and some routers dislike a path parameter with a
		// suffix, so it is stripped here instead.
		name := strings.TrimSuffix(chi.URLParam(req, "schema"), ".json")
		schema, ok := schemas[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		buf, err := json.Marshal(schema)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(rewriteSchemaRefs(buf)) //nolint:errcheck // best-effort write to HTTP client
	})
}

// rewriteSchemaRefs turns every in-document component reference into the
// URL this route serves that component under, so a nested $ref resolves
// the same way the top-level $schema URL does. Without it a caller that
// followed a $schema URL would land on a schema whose own references point
// into a document it does not have.
//
// The scan is a literal one because the grammar is literal: a fixed prefix
// followed by a component name that runs to the closing quote.
func rewriteSchemaRefs(b []byte) []byte {
	prefix := []byte(schemaRefPrefix)
	out := make([]byte, 0, len(b))
	rest := b
	for {
		i := bytes.Index(rest, prefix)
		if i < 0 {
			return append(out, rest...)
		}
		out = append(out, rest[:i]...)
		rest = rest[i:]
		name := rest[len(prefix):]
		j := bytes.IndexByte(name, '"')
		if j < 0 {
			// No closing quote: the input is not the JSON this assumes, so
			// emit the remainder unchanged rather than truncating it.
			return append(out, rest...)
		}
		out = append(out, schemasRoutePath...)
		out = append(out, '/')
		out = append(out, name[:j]...)
		out = append(out, ".json"...)
		rest = name[j:]
	}
}
