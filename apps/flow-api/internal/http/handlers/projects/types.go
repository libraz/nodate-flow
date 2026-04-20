// Package projects contains Huma operation handlers for the
// /workspaces/{wsId}/projects and /projects/{prjId} endpoints.
package projects

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
	// Audit records audit log entries for project mutations.
	// Optional: nil disables audit logging.
	Audit *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// Project is the public DTO for a project row.
type Project struct {
	ID               string  `json:"id" doc:"Project public id (UUID v7)"`
	WorkspaceID      string  `json:"workspaceId"`
	Slug             string  `json:"slug"`
	Identifier       string  `json:"identifier"`
	Name             string  `json:"name"`
	Description      string  `json:"description,omitempty"`
	Color            string  `json:"color,omitempty"`
	IsArchived       bool    `json:"isArchived"`
	StartedOn        *string `json:"startedOn,omitempty" doc:"YYYY-MM-DD"`
	EndedOn          *string `json:"endedOn,omitempty" doc:"YYYY-MM-DD"`
	FeaturePages     bool    `json:"featurePages"`
	FeatureTimeboxes bool    `json:"featureTimeboxes"`
	FeatureLenses    bool    `json:"featureLenses"`
	FeatureCalendar  bool    `json:"featureCalendar"`
	UpdatedAt        *int64  `json:"updatedAt,omitempty"`
	CreatedAt        int64   `json:"createdAt"`
}

// ProjectMember is the public DTO for a project_members row.
type ProjectMember struct {
	ID          string `json:"id" doc:"ProjectMember public id (UUID v7)"`
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Role        string `json:"role"`
	AddedAt     *int64 `json:"addedAt,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// CreateProjectBody is the request body for POST /workspaces/{wsId}/projects.
type CreateProjectBody struct {
	Slug        string `json:"slug" minLength:"1" maxLength:"64"`
	Identifier  string `json:"identifier,omitempty" maxLength:"5" doc:"Human-readable project key (e.g. NF)"`
	Name        string `json:"name" minLength:"1" maxLength:"100"`
	Description string `json:"description,omitempty" maxLength:"500"`
	Color       string `json:"color,omitempty" maxLength:"32"`
}

// CreateProjectInput is the input for POST /workspaces/{wsId}/projects.
type CreateProjectInput struct {
	WsID string `path:"wsId"`
	Body CreateProjectBody
}

// CreateProjectOutput is the response for POST /workspaces/{wsId}/projects.
type CreateProjectOutput struct {
	Body Project
}

// ListProjectsInput is the query for GET /workspaces/{wsId}/projects.
type ListProjectsInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListProjectsBody is the response body shape for GET /workspaces/{wsId}/projects.
type ListProjectsBody struct {
	Total      int64     `json:"total"`
	Projects   []Project `json:"projects"`
	NextCursor *string   `json:"nextCursor"`
}

// ListProjectsOutput is the response for GET /workspaces/{wsId}/projects.
type ListProjectsOutput struct {
	Body ListProjectsBody
}

// GetProjectInput is the path for GET /projects/{prjId}.
type GetProjectInput struct {
	PrjID string `path:"prjId"`
}

// GetProjectOutput is the response for GET /projects/{prjId}.
type GetProjectOutput struct {
	Body Project
}

// PatchProjectBody is the request body for PATCH /projects/{prjId}.
type PatchProjectBody struct {
	Slug        *string `json:"slug,omitempty" minLength:"1" maxLength:"64"`
	Name        *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Description *string `json:"description,omitempty" maxLength:"500"`
}

// PatchProjectInput is the input for PATCH /projects/{prjId}.
type PatchProjectInput struct {
	PrjID string `path:"prjId"`
	Body  PatchProjectBody
}

// PatchProjectOutput is the response for PATCH /projects/{prjId}.
type PatchProjectOutput struct {
	Body Project
}

// DisableProjectInput is the path for DELETE /projects/{prjId}.
type DisableProjectInput struct {
	PrjID string `path:"prjId"`
}

// DisableProjectBody is the response body for DELETE /projects/{prjId}.
type DisableProjectBody struct {
	Ok bool `json:"ok"`
}

// DisableProjectOutput is the response for DELETE /projects/{prjId}.
type DisableProjectOutput struct {
	Body DisableProjectBody
}

// ProjectDependencyEdge is one task_dependencies row scoped to a project.
// Both endpoints are tasks that belong to the same project, so the web
// client can render arrows between their corresponding rows / bars.
type ProjectDependencyEdge struct {
	ID                   string `json:"id" doc:"Dependency public id (UUID v7)"`
	Kind                 string `json:"kind"`
	FromTaskID           string `json:"fromTaskId"`
	FromTaskDerivedState string `json:"fromTaskDerivedState"`
	ToTaskID             string `json:"toTaskId"`
	ToTaskDerivedState   string `json:"toTaskDerivedState"`
}

// ListProjectDependenciesInput is the path for GET /projects/{prjId}/dependencies.
type ListProjectDependenciesInput struct {
	PrjID string `path:"prjId"`
}

// ListProjectDependenciesBody is the response payload for GET /projects/{prjId}/dependencies.
type ListProjectDependenciesBody struct {
	Edges []ProjectDependencyEdge `json:"edges"`
}

// ListProjectDependenciesOutput is the response for GET /projects/{prjId}/dependencies.
type ListProjectDependenciesOutput struct {
	Body ListProjectDependenciesBody
}

// ListProjectMembersInput is the query for GET /projects/{prjId}/members.
type ListProjectMembersInput struct {
	PrjID  string `path:"prjId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListProjectMembersBody is the response body for GET /projects/{prjId}/members.
type ListProjectMembersBody struct {
	Total      int64           `json:"total"`
	Members    []ProjectMember `json:"members"`
	NextCursor *string         `json:"nextCursor"`
}

// ListProjectMembersOutput is the response for GET /projects/{prjId}/members.
type ListProjectMembersOutput struct {
	Body ListProjectMembersBody
}

// AddProjectMemberBody is the request body for POST /projects/{prjId}/members.
type AddProjectMemberBody struct {
	UserID string `json:"userId" doc:"User public id (UUID v7)"`
	Role   string `json:"role" enum:"lead,editor,commenter,viewer"`
}

// AddProjectMemberInput is the input for POST /projects/{prjId}/members.
type AddProjectMemberInput struct {
	PrjID string `path:"prjId"`
	Body  AddProjectMemberBody
}

// AddProjectMemberOutput is the response for POST /projects/{prjId}/members.
type AddProjectMemberOutput struct {
	Body ProjectMember
}

// RemoveProjectMemberInput is the path for DELETE /projects/{prjId}/members/{userId}.
type RemoveProjectMemberInput struct {
	PrjID  string `path:"prjId"`
	UserID string `path:"userId"`
}

// RemoveProjectMemberBody is the response body for DELETE /projects/{prjId}/members/{userId}.
type RemoveProjectMemberBody struct {
	Ok bool `json:"ok"`
}

// RemoveProjectMemberOutput is the response for DELETE /projects/{prjId}/members/{userId}.
type RemoveProjectMemberOutput struct {
	Body RemoveProjectMemberBody
}

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// nullTimeDate delegates to handlerutil.NullTimeDate (returns *string YYYY-MM-DD, nil for NULL).
var nullTimeDate = handlerutil.NullTimeDate

// int64Ptr returns a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}
