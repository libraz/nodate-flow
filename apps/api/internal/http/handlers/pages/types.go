// Package pages contains Huma operation handlers for the
// /workspaces/{wsId}/pages endpoints (wiki / documentation page CRUD,
// hierarchy management, search, and AI generation).
package pages

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// MaxPageDepth is the maximum nesting depth allowed for page hierarchies.
const MaxPageDepth = 5

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullStrPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

// nullTimeUnix converts a sql.NullTime to a unix-seconds int64.
// Returns 0 for the NULL case.
func nullTimeUnix(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// ptrStr safely dereferences a *string, returning "" if nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// PageDTO is the public DTO for a page row.
type PageDTO struct {
	ID                 string  `json:"id" doc:"Page public id (UUID v7)"`
	ProjectID          *string `json:"projectId,omitempty" doc:"Project public id"`
	ProjectName        *string `json:"projectName,omitempty"`
	CreatorID          string  `json:"creatorId" doc:"Creator public id"`
	CreatorDisplayName string  `json:"creatorDisplayName"`
	ParentPageID       *string `json:"parentPageId,omitempty" doc:"Parent page public id"`
	ParentPageTitle    *string `json:"parentPageTitle,omitempty"`
	Title              string  `json:"title"`
	Body               string  `json:"body"`
	IsAIGenerated      bool    `json:"isAiGenerated"`
	SortWeight         int32   `json:"sortWeight"`
	Notes              *string `json:"notes,omitempty"`
	UpdatedAt          int64   `json:"updatedAt" doc:"Unix seconds"`
	CreatedAt          int64   `json:"createdAt" doc:"Unix seconds"`
}

// PageSummaryDTO is a lightweight DTO used in list endpoints (no body field).
type PageSummaryDTO struct {
	ID                 string  `json:"id" doc:"Page public id (UUID v7)"`
	ProjectID          *string `json:"projectId,omitempty" doc:"Project public id"`
	ProjectName        *string `json:"projectName,omitempty"`
	CreatorID          string  `json:"creatorId" doc:"Creator public id"`
	CreatorDisplayName string  `json:"creatorDisplayName"`
	ParentPageID       *string `json:"parentPageId,omitempty" doc:"Parent page public id"`
	Title              string  `json:"title"`
	IsAIGenerated      bool    `json:"isAiGenerated"`
	SortWeight         int32   `json:"sortWeight"`
	UpdatedAt          int64   `json:"updatedAt" doc:"Unix seconds"`
	CreatedAt          int64   `json:"createdAt" doc:"Unix seconds"`
}

// --- Create ---

// CreatePageBody is the request body for POST /workspaces/{wsId}/pages.
type CreatePageBody struct {
	Title        string  `json:"title" minLength:"1" maxLength:"500"`
	Body         string  `json:"body,omitempty" maxLength:"100000"`
	ProjectID    *string `json:"projectId,omitempty" doc:"Project public id"`
	ParentPageID *string `json:"parentPageId,omitempty" doc:"Parent page public id"`
}

// CreatePageInput is the input for POST /workspaces/{wsId}/pages.
type CreatePageInput struct {
	WsID string `path:"wsId"`
	Body CreatePageBody
}

// CreatePageOutput is the response for POST /workspaces/{wsId}/pages.
type CreatePageOutput struct {
	Body PageDTO
}

// --- List ---

// ListPagesInput is the query for GET /workspaces/{wsId}/pages.
type ListPagesInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListPagesBody is the response body for GET /workspaces/{wsId}/pages.
type ListPagesBody struct {
	Total int64            `json:"total"`
	Pages []PageSummaryDTO `json:"pages"`
}

// ListPagesOutput is the response for GET /workspaces/{wsId}/pages.
type ListPagesOutput struct {
	Body ListPagesBody
}

// --- ListChildren ---

// ListChildPagesInput is the query for GET /workspaces/{wsId}/pages/{pageId}/children.
type ListChildPagesInput struct {
	WsID   string `path:"wsId"`
	PageID string `path:"pageId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListChildPagesBody is the response body for GET /workspaces/{wsId}/pages/{pageId}/children.
type ListChildPagesBody struct {
	Total int64            `json:"total"`
	Pages []PageSummaryDTO `json:"pages"`
}

// ListChildPagesOutput is the response for GET /workspaces/{wsId}/pages/{pageId}/children.
type ListChildPagesOutput struct {
	Body ListChildPagesBody
}

// --- Get ---

// GetPageInput is the path for GET /workspaces/{wsId}/pages/{pageId}.
type GetPageInput struct {
	WsID   string `path:"wsId"`
	PageID string `path:"pageId"`
}

// GetPageOutput is the response for GET /workspaces/{wsId}/pages/{pageId}.
type GetPageOutput struct {
	Body PageDTO
}

// --- Update ---

// UpdatePageBody is the request body for PATCH /workspaces/{wsId}/pages/{pageId}.
type UpdatePageBody struct {
	Title        *string `json:"title,omitempty" minLength:"1" maxLength:"500"`
	Body         *string `json:"body,omitempty" maxLength:"100000"`
	ProjectID    *string `json:"projectId,omitempty" doc:"Project public id; null to unset"`
	ParentPageID *string `json:"parentPageId,omitempty" doc:"Parent page public id; null to unset"`
}

// UpdatePageInput is the input for PATCH /workspaces/{wsId}/pages/{pageId}.
type UpdatePageInput struct {
	WsID   string `path:"wsId"`
	PageID string `path:"pageId"`
	Body   UpdatePageBody
}

// UpdatePageOutput is the response for PATCH /workspaces/{wsId}/pages/{pageId}.
type UpdatePageOutput struct {
	Body PageDTO
}

// --- Delete ---

// DeletePageInput is the path for DELETE /workspaces/{wsId}/pages/{pageId}.
type DeletePageInput struct {
	WsID   string `path:"wsId"`
	PageID string `path:"pageId"`
}

// DeletePageBody is the response body for DELETE /workspaces/{wsId}/pages/{pageId}.
type DeletePageBody struct {
	Ok bool `json:"ok"`
}

// DeletePageOutput is the response for DELETE /workspaces/{wsId}/pages/{pageId}.
type DeletePageOutput struct {
	Body DeletePageBody
}

// --- Search ---

// SearchPagesInput is the query for GET /workspaces/{wsId}/pages/search.
type SearchPagesInput struct {
	WsID   string `path:"wsId"`
	Q      string `query:"q" minLength:"1" maxLength:"200" doc:"Search term for title"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// SearchPagesBody is the response body for GET /workspaces/{wsId}/pages/search.
type SearchPagesBody struct {
	Total int64            `json:"total"`
	Pages []PageSummaryDTO `json:"pages"`
}

// SearchPagesOutput is the response for GET /workspaces/{wsId}/pages/search.
type SearchPagesOutput struct {
	Body SearchPagesBody
}

// --- GenerateWithAI ---

// GeneratePageBody is the request body for POST /workspaces/{wsId}/pages/generate.
type GeneratePageBody struct {
	Title     string  `json:"title" minLength:"1" maxLength:"500" doc:"Title for the generated page"`
	Prompt    string  `json:"prompt" minLength:"1" maxLength:"10000" doc:"Instructions for AI generation"`
	ProjectID *string `json:"projectId,omitempty" doc:"Scope to a project"`
}

// GeneratePageInput is the input for POST /workspaces/{wsId}/pages/generate.
type GeneratePageInput struct {
	WsID string `path:"wsId"`
	Body GeneratePageBody
}

// GeneratePageOutput is the response for POST /workspaces/{wsId}/pages/generate.
type GeneratePageOutput struct {
	Body PageDTO
}
