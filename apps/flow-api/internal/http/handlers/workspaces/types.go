// Package workspaces contains Huma operation handlers for the
// /workspaces and /workspaces/{wsId}/members endpoints.
package workspaces

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Audit records audit log entries for workspace mutations.
	// Optional: nil disables audit logging.
	Audit *audit.Recorder
}

// httpErr converts an APIError Spec into a Huma status error so the
// canonical error envelope is emitted by the framework.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// Workspace is the public DTO for a workspace row.
type Workspace struct {
	ID          string     `json:"id" doc:"Workspace public id (UUID v7)"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	IconURL     string     `json:"iconUrl,omitempty"`
	Role        string     `json:"role,omitempty" doc:"Caller's role in this workspace"`
	MemberCount int64      `json:"memberCount" doc:"Number of enabled members in this workspace"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// WorkspaceMember is the public DTO for a workspace_members row.
type WorkspaceMember struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	AvatarURL   string     `json:"avatarUrl,omitempty"`
	Role        string     `json:"role"`
	InvitedAt   *time.Time `json:"invitedAt,omitempty"`
	JoinedAt    *time.Time `json:"joinedAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
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

// WorkspaceInvite is the public DTO for a workspace_invites row.
type WorkspaceInvite struct {
	ID            string     `json:"id" doc:"Invite public id (UUID v7)"`
	Role          string     `json:"role"`
	MaxUses       *int32     `json:"maxUses"`
	UseCount      uint32     `json:"useCount"`
	Label         string     `json:"label,omitempty"`
	CreatedByName string     `json:"createdByName"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// CreateWorkspaceInviteInput is the body for POST /workspaces/{wsId}/invites.
type CreateWorkspaceInviteInput struct {
	WsID string `path:"wsId"`
	Body CreateWorkspaceInviteInputBody
}

// CreateWorkspaceInviteInputBody is the JSON body for POST /workspaces/{wsId}/invites.
type CreateWorkspaceInviteInputBody struct {
	Role      string `json:"role" enum:"owner,admin,member,guest"`
	MaxUses   *int32 `json:"maxUses,omitempty" minimum:"1"`
	ExpiresIn *int64 `json:"expiresIn,omitempty" doc:"Seconds until invite expires" minimum:"1"`
	Label     string `json:"label,omitempty" maxLength:"200"`
	Email     string `json:"email,omitempty" maxLength:"320"`
}

// CreateWorkspaceInviteOutput is the response for POST /workspaces/{wsId}/invites.
type CreateWorkspaceInviteOutput struct {
	Body CreateWorkspaceInviteOutputBody
}

// CreateWorkspaceInviteOutputBody is the response body for POST /workspaces/{wsId}/invites.
// Token is only returned on creation; it is never stored or returned again.
type CreateWorkspaceInviteOutputBody struct {
	Invite WorkspaceInvite `json:"invite"`
	Token  string          `json:"token" doc:"Plaintext token (only returned once)"`
}

// ListWorkspaceInvitesInput is the query for GET /workspaces/{wsId}/invites.
type ListWorkspaceInvitesInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListWorkspaceInvitesOutput is the response for GET /workspaces/{wsId}/invites.
type ListWorkspaceInvitesOutput struct {
	Body ListWorkspaceInvitesOutputBody
}

// ListWorkspaceInvitesOutputBody is the response body envelope for GET /workspaces/{wsId}/invites.
type ListWorkspaceInvitesOutputBody struct {
	Total      int64             `json:"total"`
	Invites    []WorkspaceInvite `json:"invites"`
	NextCursor *string           `json:"nextCursor"`
}

// RevokeWorkspaceInviteInput is the path for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeWorkspaceInviteInput struct {
	WsID     string `path:"wsId"`
	InviteID string `path:"inviteId"`
}

// RevokeWorkspaceInviteOutput is the response for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeWorkspaceInviteOutput struct {
	Body RevokeWorkspaceInviteOutputBody
}

// RevokeWorkspaceInviteOutputBody is the response body envelope for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeWorkspaceInviteOutputBody struct {
	Ok bool `json:"ok"`
}

// AcceptWorkspaceInviteInput is the path for POST /invites/{token}/accept.
type AcceptWorkspaceInviteInput struct {
	Token string `path:"token"`
}

// AcceptWorkspaceInviteOutput is the response for POST /invites/{token}/accept.
type AcceptWorkspaceInviteOutput struct {
	Body AcceptWorkspaceInviteOutputBody
}

// AcceptWorkspaceInviteOutputBody is the response body for POST /invites/{token}/accept.
type AcceptWorkspaceInviteOutputBody struct {
	WorkspaceID   string `json:"workspaceId" doc:"Workspace public id for client redirect"`
	WorkspaceName string `json:"workspaceName"`
	Role          string `json:"role"`
}

// InviteInfoInput is the path for GET /invites/{token}/info.
type InviteInfoInput struct {
	Token string `path:"token"`
}

// InviteInfoOutput is the response for GET /invites/{token}/info.
type InviteInfoOutput struct {
	Body InviteInfoOutputBody
}

// InviteInfoOutputBody is the response body for GET /invites/{token}/info.
type InviteInfoOutputBody struct {
	WorkspaceName string     `json:"workspaceName"`
	Role          string     `json:"role"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
}

// nullStr converts a sql.NullString to a plain string (empty when NULL).
func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// nullTime converts a sql.NullTime to a *time.Time, returning nil when
// the column is NULL so the field is omitted from JSON instead of being
// serialised as Go zero-time ("0001-01-01T00:00:00Z").
func nullTime(t sql.NullTime) *time.Time {
	if t.Valid {
		v := t.Time
		return &v
	}
	return nil
}

// timePtr wraps a non-null time.Time so it can be assigned to a
// *time.Time DTO field.
func timePtr(t time.Time) *time.Time {
	return &t
}
