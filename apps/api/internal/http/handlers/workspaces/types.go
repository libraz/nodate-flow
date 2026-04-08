// Package workspaces contains Huma operation handlers for the
// /workspaces and /workspaces/{wsId}/members endpoints.
package workspaces

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

// httpErr converts an APIError Spec into a Huma status error so the
// canonical error envelope is emitted by the framework.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// Workspace is the public DTO for a workspace row.
type Workspace struct {
	ID          string    `json:"id" doc:"Workspace public id (UUID v7)"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	IconURL     string    `json:"iconUrl,omitempty"`
	Role        string    `json:"role,omitempty" doc:"Caller's role in this workspace"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// WorkspaceMember is the public DTO for a workspace_members row.
type WorkspaceMember struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	Role        string    `json:"role"`
	InvitedAt   time.Time `json:"invitedAt,omitempty"`
	JoinedAt    time.Time `json:"joinedAt,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateWorkspaceInput is the body for POST /workspaces.
type CreateWorkspaceInput struct {
	Body CreateWorkspaceInputBody
}

// CreateWorkspaceInputBody is the JSON body for POST /workspaces.
type CreateWorkspaceInputBody struct {
	Slug        string `json:"slug" minLength:"1" maxLength:"64"`
	Name        string `json:"name" minLength:"1" maxLength:"100"`
	Description string `json:"description,omitempty" maxLength:"500"`
	IconURL     string `json:"iconUrl,omitempty" maxLength:"500"`
}

// CreateWorkspaceOutput is the response for POST /workspaces.
type CreateWorkspaceOutput struct {
	Body Workspace
}

// ListWorkspacesInput is the query for GET /workspaces.
type ListWorkspacesInput struct {
	Limit  int32 `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// ListWorkspacesOutput is the response for GET /workspaces.
type ListWorkspacesOutput struct {
	Body ListWorkspacesOutputBody
}

// ListWorkspacesOutputBody is the response body envelope for GET /workspaces.
type ListWorkspacesOutputBody struct {
	Total      int64       `json:"total"`
	Workspaces []Workspace `json:"workspaces"`
	NextCursor *string     `json:"nextCursor"`
}

// GetWorkspaceInput is the path for GET /workspaces/{wsId}.
type GetWorkspaceInput struct {
	WsID string `path:"wsId"`
}

// GetWorkspaceOutput is the response for GET /workspaces/{wsId}.
type GetWorkspaceOutput struct {
	Body Workspace
}

// PatchWorkspaceInput is the body for PATCH /workspaces/{wsId}.
type PatchWorkspaceInput struct {
	WsID string `path:"wsId"`
	Body PatchWorkspaceInputBody
}

// PatchWorkspaceInputBody is the JSON body for PATCH /workspaces/{wsId}.
type PatchWorkspaceInputBody struct {
	Slug        *string `json:"slug,omitempty" minLength:"1" maxLength:"64"`
	Name        *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Description *string `json:"description,omitempty" maxLength:"500"`
	IconURL     *string `json:"iconUrl,omitempty" maxLength:"500"`
}

// PatchWorkspaceOutput is the response for PATCH /workspaces/{wsId}.
type PatchWorkspaceOutput struct {
	Body Workspace
}

// DisableWorkspaceInput is the path for DELETE /workspaces/{wsId}.
type DisableWorkspaceInput struct {
	WsID string `path:"wsId"`
}

// DisableWorkspaceOutput is the response for DELETE /workspaces/{wsId}.
type DisableWorkspaceOutput struct {
	Body DisableWorkspaceOutputBody
}

// DisableWorkspaceOutputBody is the response body envelope for DELETE /workspaces/{wsId}.
type DisableWorkspaceOutputBody struct {
	Ok bool `json:"ok"`
}

// ListWorkspaceMembersInput is the query for GET /workspaces/{wsId}/members.
type ListWorkspaceMembersInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListWorkspaceMembersOutput is the response for GET /workspaces/{wsId}/members.
type ListWorkspaceMembersOutput struct {
	Body ListWorkspaceMembersOutputBody
}

// ListWorkspaceMembersOutputBody is the response body envelope for GET /workspaces/{wsId}/members.
type ListWorkspaceMembersOutputBody struct {
	Total      int64             `json:"total"`
	Members    []WorkspaceMember `json:"members"`
	NextCursor *string           `json:"nextCursor"`
}

// WorkspaceUserSummary is a minimal user DTO returned by
// GET /workspaces/{wsId}/users for actor-filter pickers.
type WorkspaceUserSummary struct {
	ID          string `json:"id" doc:"User public id (UUID v7)"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
}

// ListWorkspaceUsersInput is the path for GET /workspaces/{wsId}/users.
type ListWorkspaceUsersInput struct {
	WsID string `path:"wsId"`
}

// ListWorkspaceUsersOutput is the response for GET /workspaces/{wsId}/users.
type ListWorkspaceUsersOutput struct {
	Body ListWorkspaceUsersOutputBody
}

// ListWorkspaceUsersOutputBody is the response body envelope for
// GET /workspaces/{wsId}/users.
type ListWorkspaceUsersOutputBody struct {
	Users []WorkspaceUserSummary `json:"users"`
}

// AddWorkspaceMemberInput is the body for POST /workspaces/{wsId}/members.
type AddWorkspaceMemberInput struct {
	WsID string `path:"wsId"`
	Body AddWorkspaceMemberInputBody
}

// AddWorkspaceMemberInputBody is the JSON body for POST /workspaces/{wsId}/members.
type AddWorkspaceMemberInputBody struct {
	Email string `json:"email" format:"email"`
	Role  string `json:"role" enum:"owner,admin,member,guest"`
}

// AddWorkspaceMemberOutput is the response for POST /workspaces/{wsId}/members.
type AddWorkspaceMemberOutput struct {
	Body WorkspaceMember
}

// UpdateWorkspaceMemberRoleInput is the body for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateWorkspaceMemberRoleInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
	Body   UpdateWorkspaceMemberRoleInputBody
}

// UpdateWorkspaceMemberRoleInputBody is the JSON body for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateWorkspaceMemberRoleInputBody struct {
	Role string `json:"role" enum:"owner,admin,member,guest"`
}

// UpdateWorkspaceMemberRoleOutput is the response for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateWorkspaceMemberRoleOutput struct {
	Body WorkspaceMember
}

// RemoveWorkspaceMemberInput is the path for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveWorkspaceMemberInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
}

// RemoveWorkspaceMemberOutput is the response for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveWorkspaceMemberOutput struct {
	Body RemoveWorkspaceMemberOutputBody
}

// RemoveWorkspaceMemberOutputBody is the response body envelope for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveWorkspaceMemberOutputBody struct {
	Ok bool `json:"ok"`
}

// nullStr converts a sql.NullString to a plain string (empty when NULL).
func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// nullTime converts a sql.NullTime to a plain time (zero when NULL).
func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
