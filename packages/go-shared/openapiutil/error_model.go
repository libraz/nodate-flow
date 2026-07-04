package openapiutil

import "github.com/danielgtaylor/huma/v2"

// PatchErrorModelSchema makes Huma's built-in ErrorModel schema match
// the ProblemDetails envelope emitted by the APIs.
func PatchErrorModelSchema(spec *huma.OpenAPI) {
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
	schema.Properties["extensions"] = &huma.Schema{
		Type:                 "object",
		AdditionalProperties: true,
		Description:          "Optional RFC 9457 extension members carrying diagnostic detail.",
	}
}
