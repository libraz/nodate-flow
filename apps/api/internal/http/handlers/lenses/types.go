// Package lenses contains Huma operation handlers for the
// /workspaces/{wsId}/lenses endpoints (saved view CRUD).
package lenses

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// SavedLens is the public DTO for a saved lens row.
type SavedLens struct {
	ID                 string          `json:"id" doc:"Lens public id (UUID v7)"`
	CreatorID          string          `json:"creatorId"`
	CreatorDisplayName string          `json:"creatorDisplayName"`
	Name               string          `json:"name"`
	Filter             json.RawMessage `json:"filter"`
	Sort               json.RawMessage `json:"sort"`
	GroupBy            *string         `json:"groupBy"`
	IsDefault          bool            `json:"isDefault"`
	IsPublic           bool            `json:"isPublic"`
	PublicToken        *string         `json:"publicToken,omitempty" doc:"Share token; only returned to workspace members"`
	SharedAt           *int64          `json:"sharedAt,omitempty" doc:"Unix seconds when first shared publicly"`
	SafetyCheckedAt    *int64          `json:"safetyCheckedAt,omitempty" doc:"Unix seconds of last AI safety check"`
	SortWeight         int32           `json:"sortWeight"`
	UpdatedAt          *time.Time      `json:"updatedAt,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
}

// PublicLens is the read-only DTO returned by the unauthenticated
// public share endpoint. It intentionally omits creator, workspace,
// and internal metadata.
type PublicLens struct {
	ID      string          `json:"id" doc:"Lens public id (UUID v7)"`
	Name    string          `json:"name"`
	Filter  json.RawMessage `json:"filter"`
	Sort    json.RawMessage `json:"sort"`
	GroupBy *string         `json:"groupBy"`
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

// PublishLensBody is the response body for publishing a lens.
type PublishLensBody struct {
	PublicToken string `json:"publicToken" doc:"32-char hex token for the public share URL"`
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
type GetPublicLensInput struct {
	Token string `path:"token" minLength:"32" maxLength:"32" doc:"32-char hex share token"`
}

// GetPublicLensOutput is the response for GET /public/lenses/{token}.
type GetPublicLensOutput struct {
	Body PublicLens
}

// CreateLensBody is the request body for POST /workspaces/{wsId}/lenses.
type CreateLensBody struct {
	ProjectID *string         `json:"projectId,omitempty" doc:"Project public id; omit for workspace-wide"`
	Name      string          `json:"name" minLength:"1" maxLength:"100"`
	Filter    json.RawMessage `json:"filter"`
	Sort      json.RawMessage `json:"sort"`
	GroupBy   *string         `json:"groupBy,omitempty"`
	IsDefault bool            `json:"isDefault"`
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
	WsID      string  `path:"wsId"`
	ProjectID string `query:"projectId" doc:"Filter by project public id"`
	Limit     int32   `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset    int32   `query:"offset" minimum:"0" default:"0"`
}

// ListLensesBody is the response body for GET /workspaces/{wsId}/lenses.
type ListLensesBody struct {
	Total  int64  `json:"total"`
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
	Name      *string          `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Filter    *json.RawMessage `json:"filter,omitempty"`
	Sort      *json.RawMessage `json:"sort,omitempty"`
	GroupBy   *string          `json:"groupBy,omitempty"`
	IsDefault *bool            `json:"isDefault,omitempty"`
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

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}
