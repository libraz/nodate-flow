// Package dashboard contains Huma operation handlers for the
// /workspaces/{wsId}/dashboard/widgets endpoints (widget CRUD,
// position updates, soft-delete).
package dashboard

import (
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// WidgetDTO is the public DTO for a dashboard widget row.
type WidgetDTO struct {
	ID                 string          `json:"id" doc:"Widget public id (UUID v7)"`
	CreatorID          string          `json:"creatorId"`
	CreatorDisplayName string          `json:"creatorDisplayName"`
	WidgetType         string          `json:"widgetType"`
	Title              string          `json:"title"`
	Config             json.RawMessage `json:"config,omitempty"`
	PositionX          int             `json:"positionX"`
	PositionY          int             `json:"positionY"`
	Width              int             `json:"width"`
	Height             int             `json:"height"`
	SortWeight         int             `json:"sortWeight"`
	UpdatedAt          int64           `json:"updatedAt"`
	CreatedAt          int64           `json:"createdAt"`
}

// --- Create ---

// CreateWidgetBody is the request body for POST /workspaces/{wsId}/dashboard/widgets.
type CreateWidgetBody struct {
	WidgetType string          `json:"widgetType" minLength:"1" maxLength:"50" doc:"Widget type enum value"`
	Title      string          `json:"title" minLength:"1" maxLength:"200"`
	Config     json.RawMessage `json:"config,omitempty" doc:"Arbitrary JSON configuration blob"`
	PositionX  int             `json:"positionX" minimum:"0"`
	PositionY  int             `json:"positionY" minimum:"0"`
	Width      int             `json:"width" minimum:"1"`
	Height     int             `json:"height" minimum:"1"`
}

// CreateWidgetInput is the input for POST /workspaces/{wsId}/dashboard/widgets.
type CreateWidgetInput struct {
	WsID string `path:"wsId"`
	Body CreateWidgetBody
}

// CreateWidgetOutput is the response for POST /workspaces/{wsId}/dashboard/widgets.
type CreateWidgetOutput struct {
	Body WidgetDTO
}

// --- List ---

// ListWidgetsInput is the query for GET /workspaces/{wsId}/dashboard/widgets.
type ListWidgetsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListWidgetsBody is the response body for GET /workspaces/{wsId}/dashboard/widgets.
type ListWidgetsBody struct {
	Total   int64       `json:"total"`
	Widgets []WidgetDTO `json:"widgets"`
}

// ListWidgetsOutput is the response for GET /workspaces/{wsId}/dashboard/widgets.
type ListWidgetsOutput struct {
	Body ListWidgetsBody
}

// --- Get ---

// GetWidgetInput is the path for GET /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type GetWidgetInput struct {
	WsID     string `path:"wsId"`
	WidgetID string `path:"widgetId"`
}

// GetWidgetOutput is the response for GET /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type GetWidgetOutput struct {
	Body WidgetDTO
}

// --- Update ---

// UpdateWidgetBody is the request body for PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type UpdateWidgetBody struct {
	Title     *string          `json:"title,omitempty" minLength:"1" maxLength:"200"`
	Config    *json.RawMessage `json:"config,omitempty" doc:"Arbitrary JSON configuration blob"`
	PositionX *int             `json:"positionX,omitempty" minimum:"0"`
	PositionY *int             `json:"positionY,omitempty" minimum:"0"`
	Width     *int             `json:"width,omitempty" minimum:"1"`
	Height    *int             `json:"height,omitempty" minimum:"1"`
}

// UpdateWidgetInput is the input for PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type UpdateWidgetInput struct {
	WsID     string `path:"wsId"`
	WidgetID string `path:"widgetId"`
	Body     UpdateWidgetBody
}

// UpdateWidgetOutput is the response for PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type UpdateWidgetOutput struct {
	Body WidgetDTO
}

// --- UpdatePosition ---

// UpdateWidgetPositionBody is the request body for PUT /workspaces/{wsId}/dashboard/widgets/{widgetId}/position.
type UpdateWidgetPositionBody struct {
	PositionX  int `json:"positionX" minimum:"0"`
	PositionY  int `json:"positionY" minimum:"0"`
	Width      int `json:"width" minimum:"1"`
	Height     int `json:"height" minimum:"1"`
	SortWeight int `json:"sortWeight"`
}

// UpdateWidgetPositionInput is the input for PUT /workspaces/{wsId}/dashboard/widgets/{widgetId}/position.
type UpdateWidgetPositionInput struct {
	WsID     string `path:"wsId"`
	WidgetID string `path:"widgetId"`
	Body     UpdateWidgetPositionBody
}

// UpdateWidgetPositionOutput is the response for PUT /workspaces/{wsId}/dashboard/widgets/{widgetId}/position.
type UpdateWidgetPositionOutput struct {
	Body WidgetDTO
}

// --- Delete ---

// DeleteWidgetInput is the path for DELETE /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type DeleteWidgetInput struct {
	WsID     string `path:"wsId"`
	WidgetID string `path:"widgetId"`
}

// DeleteWidgetBody is the response body for DELETE /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type DeleteWidgetBody struct {
	Ok bool `json:"ok"`
}

// DeleteWidgetOutput is the response for DELETE /workspaces/{wsId}/dashboard/widgets/{widgetId}.
type DeleteWidgetOutput struct {
	Body DeleteWidgetBody
}
