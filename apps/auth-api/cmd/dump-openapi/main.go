// Command dump-openapi builds the auth-api HTTP router with stub
// dependencies and writes the merged OpenAPI 3.1 document to disk so
// the TypeScript SDK codegen pipeline can consume it without running a
// live server.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/router"
)

func main() {
	out := flag.String("o", "packages/sdk/openapi-auth.json", "output path for the OpenAPI JSON document")
	flag.Parse()

	// Stub JWT issuer: dump-openapi never signs anything, it just needs a
	// non-nil value so middleware wires up cleanly at build time.
	issuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: jwt issuer: %v\n", err)
		os.Exit(1)
	}

	res := router.BuildResult(router.Deps{JWT: issuer})
	merged := mergeSpecs(res.APIs)
	patchErrorModelSchema(merged)

	buf, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(buf, '\n'), 0o644); err != nil { //nolint:gosec // codegen artifact
		fmt.Fprintf(os.Stderr, "dump-openapi: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("dump-openapi: wrote %s (%d paths)\n", *out, len(merged.Paths))
}

// mergeSpecs merges every sub-API's OpenAPI document into a single spec.
func mergeSpecs(apis []huma.API) *huma.OpenAPI {
	if len(apis) == 0 {
		return &huma.OpenAPI{OpenAPI: "3.1.0"}
	}
	root := apis[0].OpenAPI()
	if root.Paths == nil {
		root.Paths = map[string]*huma.PathItem{}
	}
	if root.Components == nil {
		root.Components = &huma.Components{}
	}
	for _, a := range apis[1:] {
		spec := a.OpenAPI()
		for path, item := range spec.Paths {
			if existing, ok := root.Paths[path]; ok {
				mergePathItem(existing, item)
			} else {
				root.Paths[path] = item
			}
		}
		if spec.Components == nil {
			continue
		}
		if spec.Components.Schemas != nil && root.Components.Schemas != nil {
			rootMap := root.Components.Schemas.Map()
			for name, schema := range spec.Components.Schemas.Map() {
				if _, ok := rootMap[name]; ok {
					continue
				}
				rootMap[name] = schema
			}
		}
	}
	patchErrorModelSchema(root)
	return root
}

func patchErrorModelSchema(spec *huma.OpenAPI) {
	if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
		return
	}
	schema := spec.Components.Schemas.Map()["ErrorModel"]
	if schema == nil {
		return
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*huma.Schema{}
	}
	if typeSchema := schema.Properties["type"]; typeSchema != nil {
		typeSchema.Format = ""
	}
	schema.Properties["description"] = &huma.Schema{
		Type:        "string",
		Description: "Developer-facing explanation of when this error fires.",
	}
	schema.Properties["userAction"] = &huma.Schema{
		Type:        "string",
		Description: "Short imperative the UI can render to tell the end user how to recover.",
	}
}

func mergePathItem(dst, src *huma.PathItem) {
	if dst.Get == nil {
		dst.Get = src.Get
	}
	if dst.Put == nil {
		dst.Put = src.Put
	}
	if dst.Post == nil {
		dst.Post = src.Post
	}
	if dst.Delete == nil {
		dst.Delete = src.Delete
	}
	if dst.Patch == nil {
		dst.Patch = src.Patch
	}
	if dst.Head == nil {
		dst.Head = src.Head
	}
	if dst.Options == nil {
		dst.Options = src.Options
	}
	if dst.Trace == nil {
		dst.Trace = src.Trace
	}
}
