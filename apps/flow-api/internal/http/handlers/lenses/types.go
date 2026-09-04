// Package lenses contains Huma operation handlers for the
// /workspaces/{wsId}/lenses endpoints (saved view CRUD).
package lenses

import (
	"database/sql"
	"encoding/json"

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

// SavedLens is the public DTO for a saved lens row.
//
// There is deliberately no share-token field. Only the SHA-256 of a
// share token is stored, so the plaintext exists once, in the publish
// response, and cannot be re-read afterwards — by this endpoint or by
// anyone who reaches the row. IsPublic answers whether a share URL is
// live; recovering a lost URL means unpublishing and publishing again.
type SavedLens struct {
	ID                 string          `json:"id" doc:"Lens public id (UUID v7)"`
	CreatorID          string          `json:"creatorId"`
	CreatorDisplayName string          `json:"creatorDisplayName"`
	Name               string          `json:"name"`
	Description        *string         `json:"description,omitempty" doc:"Optional public-facing description shown on the share page"`
	Filter             json.RawMessage `json:"filter"`
	Sort               json.RawMessage `json:"sort"`
	GroupBy            *string         `json:"groupBy"`
	IsDefault          bool            `json:"isDefault"`
	IsPublic           bool            `json:"isPublic"`
	SharedAt           *int64          `json:"sharedAt,omitempty" doc:"Unix seconds when first shared publicly"`
	SafetyCheckedAt    *int64          `json:"safetyCheckedAt,omitempty" doc:"Unix seconds of last AI safety check"`
	SortWeight         int32           `json:"sortWeight"`
	UpdatedAt          *int64          `json:"updatedAt,omitempty"`
	CreatedAt          int64           `json:"createdAt"`
}

// PublicLensTask is the minimal task projection embedded in PublicLens.
// All fields are public-safe: no internal ids, no descriptions, no
// creator information, no embedding scores, no relations. Matches the
// columns rendered by the public share page table.
type PublicLensTask struct {
	ID                  string  `json:"id" doc:"Task public id (UUID v7)"`
	Title               string  `json:"title"`
	Status              string  `json:"status" doc:"Task derived state"`
	Priority            int32   `json:"priority"`
	DueOn               *string `json:"dueOn,omitempty" doc:"Due date as YYYY-MM-DD"`
	AssigneeDisplayName *string `json:"assigneeDisplayName,omitempty" doc:"Display name of the primary assignee, if any"`
}

// PublicLens is the read-only DTO returned by the unauthenticated
// public share endpoint. It intentionally omits creator, workspace,
// and internal metadata. Tasks resolved from the lens filter are
// embedded so the share page can render in a single round-trip; the
// list is hard-capped (see resolve.go) and not paginated because public
// shares are not meant to be unbounded data dumps.
//
// The lens definition itself — filter, sort, group-by — stays on the
// authenticated side. The share page renders the resolved tasks, not
// the query that selected them, and the definition names things the
// link holder was never given: user public ids under the assignee key
// and whatever free text the author saved under the search key. A
// definition that is absent from this DTO cannot leak through it,
// which is why it is omitted rather than sanitised. The resolver reads
// the stored blob directly (see resolve.go), so the shared page still
// answers the question the lens asks.
type PublicLens struct {
	ID          string           `json:"id" doc:"Lens public id (UUID v7)"`
	Name        string           `json:"name"`
	Description *string          `json:"description,omitempty" doc:"Optional public-facing description shown on the share page"`
	Tasks       []PublicLensTask `json:"tasks" doc:"Tasks matching the lens filter, capped at 200 rows"`
}

// PublishLensInput is the input for POST /workspaces/{wsId}/lenses/{lensId}/publish.
type PublishLensInput struct {
	WsID   string `path:"wsId"`
	LensID string `path:"lensId"`
}

// PublishLensOutput is the response for POST /workspaces/{wsId}/lenses/{lensId}/publish.
type PublishLensOutput struct {
	Body PublishLensBody
}

// PublishLensBody is the response body for publishing a lens. This is
// the only place the plaintext share token is ever returned; the row
// keeps only its hash.
type PublishLensBody struct {
	PublicToken string `json:"publicToken" doc:"Opaque token for the public share URL, returned exactly once"`
}

// UnpublishLensInput is the input for POST /workspaces/{wsId}/lenses/{lensId}/unpublish.
type UnpublishLensInput struct {
	WsID   string `path:"wsId"`
	LensID string `path:"lensId"`
}

// UnpublishLensOutput is the response for POST /workspaces/{wsId}/lenses/{lensId}/unpublish.
type UnpublishLensOutput struct {
	Body UnpublishLensBody
}

// UnpublishLensBody is the response body for unpublishing a lens.
type UnpublishLensBody struct {
	Ok bool `json:"ok"`
}

// GetPublicLensInput is the input for GET /public/lenses/{token}.
//
// The token is not length-constrained: the handler hashes whatever it
// receives and looks that up, so a wrong-shaped token gets the same
// answer as a wrong token instead of a validation error that would
// leak the expected format.
type GetPublicLensInput struct {
	Token string `path:"token" doc:"Opaque share token from the public URL"`
}

// GetPublicLensOutput is the response for GET /public/lenses/{token}.
type GetPublicLensOutput struct {
	Body PublicLens
}

// CreateLensBody is the request body for POST /workspaces/{wsId}/lenses.
type CreateLensBody struct {
	ProjectID   *string         `json:"projectId,omitempty" doc:"Project public id; omit for workspace-wide"`
	Name        string          `json:"name" minLength:"1" maxLength:"100"`
	Description *string         `json:"description,omitempty" maxLength:"500" doc:"Optional public-facing description shown on the share page"`
	Filter      json.RawMessage `json:"filter"`
	Sort        json.RawMessage `json:"sort"`
	GroupBy     *string         `json:"groupBy,omitempty"`
	IsDefault   bool            `json:"isDefault"`
}

// CreateLensInput is the input for POST /workspaces/{wsId}/lenses.
type CreateLensInput struct {
	WsID string `path:"wsId"`
	Body CreateLensBody
}

// CreateLensOutput is the response for POST /workspaces/{wsId}/lenses.
type CreateLensOutput struct {
	Body SavedLens
}

// ListLensesInput is the query for GET /workspaces/{wsId}/lenses.
type ListLensesInput struct {
	WsID      string `path:"wsId"`
	ProjectID string `query:"projectId" doc:"Filter by project public id"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset    int32  `query:"offset" minimum:"0" default:"0"`
}

// ListLensesBody is the response body for GET /workspaces/{wsId}/lenses.
type ListLensesBody struct {
	Total  int64       `json:"total"`
	Lenses []SavedLens `json:"lenses"`
}

// ListLensesOutput is the response for GET /workspaces/{wsId}/lenses.
type ListLensesOutput struct {
	Body ListLensesBody
}

// GetLensInput is the path for GET /workspaces/{wsId}/lenses/{lensId}.
type GetLensInput struct {
	WsID   string `path:"wsId"`
	LensID string `path:"lensId"`
}

// GetLensOutput is the response for GET /workspaces/{wsId}/lenses/{lensId}.
type GetLensOutput struct {
	Body SavedLens
}

// UpdateLensBody is the request body for PATCH /workspaces/{wsId}/lenses/{lensId}.
type UpdateLensBody struct {
	Name        *string          `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Description *string          `json:"description,omitempty" maxLength:"500" doc:"Optional public-facing description shown on the share page; pass null to clear"`
	Filter      *json.RawMessage `json:"filter,omitempty"`
	Sort        *json.RawMessage `json:"sort,omitempty"`
	GroupBy     *string          `json:"groupBy,omitempty"`
	IsDefault   *bool            `json:"isDefault,omitempty"`
}

// UpdateLensInput is the input for PATCH /workspaces/{wsId}/lenses/{lensId}.
type UpdateLensInput struct {
	WsID   string `path:"wsId"`
	LensID string `path:"lensId"`
	Body   UpdateLensBody
}

// UpdateLensOutput is the response for PATCH /workspaces/{wsId}/lenses/{lensId}.
type UpdateLensOutput struct {
	Body SavedLens
}

// DeleteLensInput is the path for DELETE /workspaces/{wsId}/lenses/{lensId}.
type DeleteLensInput struct {
	WsID   string `path:"wsId"`
	LensID string `path:"lensId"`
}

// DeleteLensOutput is the response for DELETE /workspaces/{wsId}/lenses/{lensId}.
type DeleteLensOutput struct {
	Body DeleteLensBody
}

// DeleteLensBody is the response body for DELETE /workspaces/{wsId}/lenses/{lensId}.
type DeleteLensBody struct {
	Ok bool `json:"ok"`
}

// nullTimeUnix and nullString are defined in mapper.go.
