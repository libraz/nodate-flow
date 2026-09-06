// Package integrationmappings contains Huma operation handlers for the
// workspace-scoped CRUD over integration_source_mappings — the table the
// /webhooks/* receivers consult to decide which tenant an inbound
// delivery belongs to. Without these routes the routing table could only
// be populated by hand in SQL, which is why the receivers used to fall
// back to a single configured workspace.
package integrationmappings

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry

// nullTimeUnix delegates to handlerutil.NullTimeUnix.
var nullTimeUnix = handlerutil.NullTimeUnix

// IntegrationMapping is the public DTO for an integration_source_mappings
// row. `externalKey` is deliberately readable: it is a public identifier
// on the provider's side (a repository id, a Slack team id, a push
// channel id we chose), not a credential, and an admin cannot tell which
// row to delete without seeing it.
type IntegrationMapping struct {
	ID          string `json:"id" doc:"Mapping public id (UUID v7)"`
	Provider    string `json:"provider" enum:"github,slack,google" doc:"Which /webhooks/* receiver this mapping routes."`
	ExternalKey string `json:"externalKey" doc:"Provider-side sender identity: github = numeric repository id, slack = team id, google = push channel id."`
	Label       string `json:"label" doc:"Display-only name for the source."`
	Enabled     bool   `json:"enabled" doc:"False pauses routing without releasing the claim on the source."`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// ListIntegrationMappingsInput is the path for GET /workspaces/{wsId}/integration-mappings.
type ListIntegrationMappingsInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
}

// ListIntegrationMappingsBody is the response payload for the list operation.
type ListIntegrationMappingsBody struct {
	Total    int64                `json:"total"`
	Mappings []IntegrationMapping `json:"mappings"`
}

// ListIntegrationMappingsOutput is the response for the list operation.
type ListIntegrationMappingsOutput struct {
	Body ListIntegrationMappingsBody
}

// CreateIntegrationMappingBody is the JSON body for POST
// /workspaces/{wsId}/integration-mappings.
type CreateIntegrationMappingBody struct {
	Provider    string `json:"provider" required:"true" enum:"github,slack,google"`
	ExternalKey string `json:"externalKey" required:"true" minLength:"1" maxLength:"255" doc:"github = numeric repository id (decimal digits, from the repository.id field of any webhook delivery), slack = team id (Txxxxxxxx), google = the channel id used when the watch was registered."`
	Label       string `json:"label" required:"true" minLength:"1" maxLength:"255" doc:"Display-only name, e.g. the owner/repo or the Slack workspace name."`
}

// CreateIntegrationMappingInput is the request for the create operation.
type CreateIntegrationMappingInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	Body CreateIntegrationMappingBody
}

// CreateIntegrationMappingOutput is the response for the create operation.
type CreateIntegrationMappingOutput struct {
	Body IntegrationMapping
}

// PatchIntegrationMappingBody is the JSON body for PATCH
// /workspaces/{wsId}/integration-mappings/{id}. provider and externalKey
// are absent by design: repointing a mapping at a different source is a
// delete plus a create, so the claim on the old source is released
// explicitly rather than silently.
type PatchIntegrationMappingBody struct {
	Label   *string `json:"label,omitempty" minLength:"1" maxLength:"255"`
	Enabled *bool   `json:"enabled,omitempty"`
}

// PatchIntegrationMappingInput is the request for the patch operation.
type PatchIntegrationMappingInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	ID   string `path:"id" doc:"Mapping public ID"`
	Body PatchIntegrationMappingBody
}

// PatchIntegrationMappingOutput is the response for the patch operation.
type PatchIntegrationMappingOutput struct {
	Body IntegrationMapping
}

// DeleteInput is the path for DELETE
// /workspaces/{wsId}/integration-mappings/{id}.
type DeleteInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	ID   string `path:"id" doc:"Mapping public ID"`
}

// IntegrationMappingDeleteOutputBody is the response body for the delete
// operation. It is named rather than inline because Huma names an anonymous
// Body after the operation alone, which makes it share one component schema
// with every other delete operation in the merged spec.
type IntegrationMappingDeleteOutputBody struct {
	Ok bool `json:"ok"`
}

// DeleteOutput is the response for the delete operation.
type DeleteOutput struct {
	Body IntegrationMappingDeleteOutputBody
}
