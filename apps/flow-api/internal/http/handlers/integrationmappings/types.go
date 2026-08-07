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

// ListInput is the path for GET /workspaces/{wsId}/integration-mappings.
type ListInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
}

// ListBody is the response payload for the list operation.
type ListBody struct {
	Total    int64                `json:"total"`
	Mappings []IntegrationMapping `json:"mappings"`
}

// ListOutput is the response for the list operation.
type ListOutput struct {
	Body ListBody
}

// CreateBody is the JSON body for POST
// /workspaces/{wsId}/integration-mappings.
type CreateBody struct {
	Provider    string `json:"provider" required:"true" enum:"github,slack,google"`
	ExternalKey string `json:"externalKey" required:"true" minLength:"1" maxLength:"255" doc:"github = numeric repository id (decimal digits, from the repository.id field of any webhook delivery), slack = team id (Txxxxxxxx), google = the channel id used when the watch was registered."`
	Label       string `json:"label" required:"true" minLength:"1" maxLength:"255" doc:"Display-only name, e.g. the owner/repo or the Slack workspace name."`
}

// CreateInput is the request for the create operation.
type CreateInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	Body CreateBody
}

// CreateOutput is the response for the create operation.
type CreateOutput struct {
	Body IntegrationMapping
}

// PatchBody is the JSON body for PATCH
// /workspaces/{wsId}/integration-mappings/{id}. provider and externalKey
// are absent by design: repointing a mapping at a different source is a
// delete plus a create, so the claim on the old source is released
// explicitly rather than silently.
type PatchBody struct {
	Label   *string `json:"label,omitempty" minLength:"1" maxLength:"255"`
	Enabled *bool   `json:"enabled,omitempty"`
}

// PatchInput is the request for the patch operation.
type PatchInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	ID   string `path:"id" doc:"Mapping public ID"`
	Body PatchBody
}

// PatchOutput is the response for the patch operation.
type PatchOutput struct {
	Body IntegrationMapping
}

// DeleteInput is the path for DELETE
// /workspaces/{wsId}/integration-mappings/{id}.
type DeleteInput struct {
	WsID string `path:"wsId" doc:"Workspace public ID"`
	ID   string `path:"id" doc:"Mapping public ID"`
}

// DeleteOutput is the response for the delete operation.
type DeleteOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
