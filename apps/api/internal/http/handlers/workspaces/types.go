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

// CreateInput is the body for POST /workspaces.
type CreateInput struct {
	Body struct {
		Slug        string `json:"slug" minLength:"1" maxLength:"64"`
		Name        string `json:"name" minLength:"1" maxLength:"100"`
		Description string `json:"description,omitempty" maxLength:"500"`
		IconURL     string `json:"iconUrl,omitempty" maxLength:"500"`
	}
}

// CreateOutput is the response for POST /workspaces.
type CreateOutput struct {
	Body Workspace
}

// ListInput is the query for GET /workspaces.
type ListInput struct {
	Limit  int32 `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// ListOutput is the response for GET /workspaces.
type ListOutput struct {
	Body struct {
		Total      int64       `json:"total"`
		Workspaces []Workspace `json:"workspaces"`
		NextCursor *string     `json:"nextCursor"`
	}
}

// GetInput is the path for GET /workspaces/{wsId}.
type GetInput struct {
	WsID string `path:"wsId"`
}

// GetOutput is the response for GET /workspaces/{wsId}.
type GetOutput struct {
	Body Workspace
}

// PatchInput is the body for PATCH /workspaces/{wsId}.
type PatchInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Slug *string `json:"slug,omitempty" minLength:"1" maxLength:"64"`
		Name *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	}
}

// PatchOutput is the response for PATCH /workspaces/{wsId}.
type PatchOutput struct {
	Body Workspace
}

// DisableInput is the path for DELETE /workspaces/{wsId}.
type DisableInput struct {
	WsID string `path:"wsId"`
}

// DisableOutput is the response for DELETE /workspaces/{wsId}.
type DisableOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ListMembersInput is the query for GET /workspaces/{wsId}/members.
type ListMembersInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListMembersOutput is the response for GET /workspaces/{wsId}/members.
type ListMembersOutput struct {
	Body struct {
		Total      int64             `json:"total"`
		Members    []WorkspaceMember `json:"members"`
		NextCursor *string           `json:"nextCursor"`
	}
}

// InviteMemberInput is the body for POST /workspaces/{wsId}/members.
type InviteMemberInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Email string `json:"email" format:"email"`
		Role  string `json:"role" enum:"owner,admin,member,guest"`
	}
}

// InviteMemberOutput is the response for POST /workspaces/{wsId}/members.
type InviteMemberOutput struct {
	Body WorkspaceMember
}

// UpdateMemberRoleInput is the body for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateMemberRoleInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
	Body   struct {
		Role string `json:"role" enum:"owner,admin,member,guest"`
	}
}

// UpdateMemberRoleOutput is the response for PATCH /workspaces/{wsId}/members/{userId}.
type UpdateMemberRoleOutput struct {
	Body WorkspaceMember
}

// RemoveMemberInput is the path for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveMemberInput struct {
	WsID   string `path:"wsId"`
	UserID string `path:"userId"`
}

// RemoveMemberOutput is the response for DELETE /workspaces/{wsId}/members/{userId}.
type RemoveMemberOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
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
