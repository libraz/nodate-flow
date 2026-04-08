// Package projects contains Huma operation handlers for the
// /workspaces/{wsId}/projects and /projects/{prjId} endpoints.
package projects

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

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// Project is the public DTO for a project row.
type Project struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Color       string    `json:"color,omitempty"`
	IsArchived  bool      `json:"isArchived"`
	StartedOn   time.Time `json:"startedOn,omitempty"`
	EndedOn     time.Time `json:"endedOn,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ProjectMember is the public DTO for a project_members row.
type ProjectMember struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	Role        string    `json:"role"`
	AddedAt     time.Time `json:"addedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateInput is the body for POST /workspaces/{wsId}/projects.
type CreateInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Slug        string `json:"slug" minLength:"1" maxLength:"64"`
		Name        string `json:"name" minLength:"1" maxLength:"100"`
		Description string `json:"description,omitempty" maxLength:"500"`
		Color       string `json:"color,omitempty" maxLength:"32"`
	}
}

// CreateOutput is the response for POST /workspaces/{wsId}/projects.
type CreateOutput struct {
	Body Project
}

// ListInput is the query for GET /workspaces/{wsId}/projects.
type ListInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutput is the response for GET /workspaces/{wsId}/projects.
type ListOutput struct {
	Body struct {
		Total      int64     `json:"total"`
		Projects   []Project `json:"projects"`
		NextCursor *string   `json:"nextCursor"`
	}
}

// GetInput is the path for GET /projects/{prjId}.
type GetInput struct {
	PrjID string `path:"prjId"`
}

// GetOutput is the response for GET /projects/{prjId}.
type GetOutput struct {
	Body Project
}

// PatchInput is the body for PATCH /projects/{prjId}.
type PatchInput struct {
	PrjID string `path:"prjId"`
	Body  struct {
		Slug        *string `json:"slug,omitempty" minLength:"1" maxLength:"64"`
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
		Description *string `json:"description,omitempty" maxLength:"500"`
	}
}

// PatchOutput is the response for PATCH /projects/{prjId}.
type PatchOutput struct {
	Body Project
}

// DisableInput is the path for DELETE /projects/{prjId}.
type DisableInput struct {
	PrjID string `path:"prjId"`
}

// DisableOutput is the response for DELETE /projects/{prjId}.
type DisableOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// ListMembersInput is the query for GET /projects/{prjId}/members.
type ListMembersInput struct {
	PrjID  string `path:"prjId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListMembersOutput is the response for GET /projects/{prjId}/members.
type ListMembersOutput struct {
	Body struct {
		Total      int64           `json:"total"`
		Members    []ProjectMember `json:"members"`
		NextCursor *string         `json:"nextCursor"`
	}
}

// AddMemberInput is the body for POST /projects/{prjId}/members.
type AddMemberInput struct {
	PrjID string `path:"prjId"`
	Body  struct {
		UserID string `json:"userId" doc:"User public id (UUID v7)"`
		Role   string `json:"role" enum:"lead,editor,commenter,viewer"`
	}
}

// AddMemberOutput is the response for POST /projects/{prjId}/members.
type AddMemberOutput struct {
	Body ProjectMember
}

// RemoveMemberInput is the path for DELETE /projects/{prjId}/members/{userId}.
type RemoveMemberInput struct {
	PrjID  string `path:"prjId"`
	UserID string `path:"userId"`
}

// RemoveMemberOutput is the response for DELETE /projects/{prjId}/members/{userId}.
type RemoveMemberOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
