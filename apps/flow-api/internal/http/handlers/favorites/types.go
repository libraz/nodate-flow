// Package favorites contains Huma operation handlers for user-scoped
// favorite CRUD (/me/favorites). Favorites are per-user bookmarks of
// workspace entities (projects, tasks, pages, lenses, timeboxes).
package favorites

import (
	"database/sql"

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

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// Favorite is the public DTO for a user favorite row.
type Favorite struct {
	ID         string `json:"id" doc:"Favorite public id (UUID v7)"`
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	FolderName string `json:"folderName,omitempty"`
	SortWeight int32  `json:"sortWeight"`
	CreatedAt  int64  `json:"createdAt"`
}

// ---- Create ----

// CreateFavoriteBody is the JSON body for POST /me/favorites.
type CreateFavoriteBody struct {
	WorkspaceID string `json:"workspaceId" doc:"Workspace public id (UUID v7)"`
	TargetType  string `json:"targetType" enum:"project,task,page,lens,timebox"`
	TargetID    string `json:"targetId" doc:"Public id (UUID v7) of the entity to favorite"`
	FolderName  string `json:"folderName,omitempty" maxLength:"64"`
}

// CreateFavoriteInput is the request for POST /me/favorites.
type CreateFavoriteInput struct {
	Body CreateFavoriteBody
}

// CreateFavoriteOutput is the response for POST /me/favorites.
type CreateFavoriteOutput struct {
	Body Favorite
}

// ---- List ----

// ListFavoritesInput is the query for GET /me/favorites.
type ListFavoritesInput struct {
	WorkspaceID string `query:"workspaceId" doc:"Workspace public id (UUID v7) to scope favorites"`
	Limit       int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset      int32  `query:"offset" minimum:"0" default:"0"`
}

// ListFavoritesBody is the response payload for GET /me/favorites.
type ListFavoritesBody struct {
	Total     int64      `json:"total"`
	Favorites []Favorite `json:"favorites"`
}

// ListFavoritesOutput is the response for GET /me/favorites.
type ListFavoritesOutput struct {
	Body ListFavoritesBody
}

// ---- Delete ----

// DeleteFavoriteInput is the path for DELETE /me/favorites/{id}.
type DeleteFavoriteInput struct {
	ID string `path:"id" doc:"Favorite public id (UUID v7)"`
}

// DeleteFavoriteOutput is the response for DELETE /me/favorites/{id}.
type DeleteFavoriteOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
