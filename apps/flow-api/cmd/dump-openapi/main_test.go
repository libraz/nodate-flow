package main

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestPatchErrorModelSchemaAddsExtensions(t *testing.T) {
	t.Parallel()

	spec := &huma.OpenAPI{
		Components: &huma.Components{
			Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer),
		},
	}
	spec.Components.Schemas.Map()["ErrorModel"] = &huma.Schema{}

	patchErrorModelSchema(spec)

	extensions := spec.Components.Schemas.Map()["ErrorModel"].Properties["extensions"]
	if extensions == nil {
		t.Fatalf("extensions schema was not added")
	}
	if extensions.Type != "object" {
		t.Fatalf("extensions type = %q, want object", extensions.Type)
	}
	if extensions.AdditionalProperties != true {
		t.Fatalf("extensions additionalProperties = %#v, want true", extensions.AdditionalProperties)
	}
}
