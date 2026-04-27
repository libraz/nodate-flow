// Package workspace contains Huma operation handlers for the
// /workspaces, /workspaces/{wsId}/members, and invite endpoints.
package workspace

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	// Audit records audit log entries for workspace mutations.
	// Optional: nil disables audit logging.
	Audit audit.Sink
}

// InviteDeps extends the standard Deps with fields required by the
// invite link handlers. EmailSender may be nil when SMTP is not
// configured; invite creation still succeeds but no email is sent.
type InviteDeps struct {
	Deps
	EmailSender email.Sender
	WebURL      string
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// Workspace is the public DTO for a workspace row.
type Workspace struct {
	ID          string `json:"id" doc:"Workspace public id (UUID v7)"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	// Timezone is the workspace-level default IANA timezone.
	Timezone string `json:"timezone"`
	// Country is the workspace-level ISO 3166-1 alpha-2 code, or empty
	// when unset. Drives the default holiday subscription.
	Country     string `json:"country"`
	Role        string `json:"role,omitempty" doc:"Caller's role in this workspace"`
	MemberCount int64  `json:"memberCount" doc:"Number of enabled members in this workspace"`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// Member is the public DTO for a workspace_members row.
type Member struct {
	ID          string `json:"id" doc:"Member public id (UUID v7)"`
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Role        string `json:"role"`
	InvitedAt   *int64 `json:"invitedAt,omitempty"`
	JoinedAt    *int64 `json:"joinedAt,omitempty"`
	UpdatedAt   *int64 `json:"updatedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
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
	// Timezone defaults to "UTC" when omitted; must be a valid IANA identifier.
	Timezone string `json:"timezone,omitempty" maxLength:"64"`
	// Country is an optional ISO 3166-1 alpha-2 code. Empty string means unset.
	Country string `json:"country,omitempty" pattern:"^$|^[A-Z]{2}$"`
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
	// Timezone is an optional IANA identifier; invalid values return 422.
	Timezone *string `json:"timezone,omitempty" maxLength:"64"`
	// Country is an optional ISO 3166-1 alpha-2 code; empty string clears it.
	Country *string `json:"country,omitempty" pattern:"^$|^[A-Z]{2}$"`
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

// ListMembersInput is the query for GET /workspaces/{wsId}/members.
type ListMembersInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListMembersOutput is the response for GET /workspaces/{wsId}/members.
type ListMembersOutput struct {
	Body ListMembersOutputBody
}

// ListMembersOutputBody is the response body envelope for GET /workspaces/{wsId}/members.
type ListMembersOutputBody struct {
	Total      int64    `json:"total"`
	Members    []Member `json:"members"`
	NextCursor *string  `json:"nextCursor"`
}

// UserSummary is a minimal user DTO returned by
// GET /workspaces/{wsId}/users for actor-filter pickers.
type UserSummary struct {
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
	Users []UserSummary `json:"users"`
}

// AddMemberInput is the body for POST /workspaces/{wsId}/members.
type AddMemberInput struct {
	WsID string `path:"wsId"`
	Body AddMemberInputBody
}

// AddMemberInputBody is the JSON body for POST /workspaces/{wsId}/members.
type AddMemberInputBody struct {
	Email string `json:"email" format:"email"`
	Role  string `json:"role" enum:"owner,admin,member,guest"`
}

// AddMemberOutput is the response for POST /workspaces/{wsId}/members.
type AddMemberOutput struct {
	Body Member
}

// UpdateMemberRoleInput is the body for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateMemberRoleInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
	Body   UpdateMemberRoleInputBody
}

// UpdateMemberRoleInputBody is the JSON body for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateMemberRoleInputBody struct {
	Role string `json:"role" enum:"owner,admin,member,guest"`
}

// UpdateMemberRoleOutput is the response for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateMemberRoleOutput struct {
	Body Member
}

// RemoveMemberInput is the path for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveMemberInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
}

// RemoveMemberOutput is the response for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveMemberOutput struct {
	Body RemoveMemberOutputBody
}

// RemoveMemberOutputBody is the response body envelope for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveMemberOutputBody struct {
	Ok bool `json:"ok"`
}

// Invite is the public DTO for a workspace_invites row.
type Invite struct {
	ID            string `json:"id" doc:"Invite public id (UUID v7)"`
	Role          string `json:"role"`
	MaxUses       *int32 `json:"maxUses"`
	UseCount      uint32 `json:"useCount"`
	Label         string `json:"label,omitempty"`
	CreatedByName string `json:"createdByName"`
	ExpiresAt     *int64 `json:"expiresAt,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

// CreateInviteInput is the body for POST /workspaces/{wsId}/invites.
type CreateInviteInput struct {
	WsID string `path:"wsId"`
	Body CreateInviteInputBody
}

// CreateInviteInputBody is the JSON body for POST /workspaces/{wsId}/invites.
type CreateInviteInputBody struct {
	Role      string `json:"role" enum:"owner,admin,member,guest"`
	MaxUses   *int32 `json:"maxUses,omitempty" minimum:"1"`
	ExpiresIn *int64 `json:"expiresIn,omitempty" doc:"Seconds until invite expires" minimum:"1"`
	Label     string `json:"label,omitempty" maxLength:"200"`
	Email     string `json:"email,omitempty" maxLength:"320"`
}

// CreateInviteOutput is the response for POST /workspaces/{wsId}/invites.
type CreateInviteOutput struct {
	Body CreateInviteOutputBody
}

// CreateInviteOutputBody is the response body for POST /workspaces/{wsId}/invites.
// Token is only returned on creation; it is never stored or returned again.
type CreateInviteOutputBody struct {
	Invite Invite `json:"invite"`
	Token  string `json:"token" doc:"Plaintext token (only returned once)"`
}

// ListInvitesInput is the query for GET /workspaces/{wsId}/invites.
type ListInvitesInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListInvitesOutput is the response for GET /workspaces/{wsId}/invites.
type ListInvitesOutput struct {
	Body ListInvitesOutputBody
}

// ListInvitesOutputBody is the response body envelope for GET /workspaces/{wsId}/invites.
type ListInvitesOutputBody struct {
	Total      int64    `json:"total"`
	Invites    []Invite `json:"invites"`
	NextCursor *string  `json:"nextCursor"`
}

// RevokeInviteInput is the path for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeInviteInput struct {
	WsID     string `path:"wsId"`
	InviteID string `path:"inviteId"`
}

// RevokeInviteOutput is the response for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeInviteOutput struct {
	Body RevokeInviteOutputBody
}

// RevokeInviteOutputBody is the response body envelope for DELETE /workspaces/{wsId}/invites/{inviteId}.
type RevokeInviteOutputBody struct {
	Ok bool `json:"ok"`
}

// AcceptInviteInput is the path for POST /invites/{token}/accept.
type AcceptInviteInput struct {
	Token string `path:"token"`
}

// AcceptInviteOutput is the response for POST /invites/{token}/accept.
type AcceptInviteOutput struct {
	Body AcceptInviteOutputBody
}

// AcceptInviteOutputBody is the response body for POST /invites/{token}/accept.
type AcceptInviteOutputBody struct {
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
	WorkspaceName string `json:"workspaceName"`
	Role          string `json:"role"`
	ExpiresAt     *int64 `json:"expiresAt,omitempty"`
}

// int64Ptr returns a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}
