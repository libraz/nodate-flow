// Package admin contains Huma operation handlers for the instance
// administration panel: user management, workspace management, session
// oversight, audit log viewing, instance admin grants, and settings.
package admin

import (
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
)

// Deps is the dependency bundle for admin handlers.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   audit.Sink
}

// --- Pagination ---

// PaginatedInput embeds limit/offset query parameters shared by all list
// endpoints.
type PaginatedInput struct {
	Limit  int32 `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32 `query:"offset" minimum:"0" default:"0"`
}

// --- Users ---

// User is the public DTO for a user in the admin panel.
type User struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"displayName"`
	AvatarURL       *string `json:"avatarUrl,omitempty"`
	Locale          string  `json:"locale"`
	LastLoginAt     *int64  `json:"lastLoginAt,omitempty"`
	EmailVerifiedAt *int64  `json:"emailVerifiedAt,omitempty"`
	Enabled         bool    `json:"enabled"`
	WorkspaceCount  int64   `json:"workspaceCount"`
	IsInstanceAdmin bool    `json:"isInstanceAdmin"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       *int64  `json:"updatedAt,omitempty"`
}

// ListUsersInput binds query parameters for GET /admin/users.
type ListUsersInput struct {
	Search  string `query:"search" default:""`
	Enabled string `query:"enabled" default:"" enum:"true,false," doc:"Filter by enabled status (empty=all)"`
	PaginatedInput
}

// ListUsersOutput is the response for GET /admin/users.
type ListUsersOutput struct {
	Body struct {
		Total int64  `json:"total"`
		Items []User `json:"items"`
	}
}

// GetUserInput binds the path parameter for GET /admin/users/{userId}.
type GetUserInput struct {
	UserID string `path:"userId"`
}

// GetUserOutput is the response for GET /admin/users/{userId}.
type GetUserOutput struct {
	Body User
}

// PatchUserInput binds the path and body for PATCH /admin/users/{userId}.
type PatchUserInput struct {
	UserID string `path:"userId"`
	Body   struct {
		Enabled *bool `json:"enabled"`
	}
}

// PatchUserOutput is the response for PATCH /admin/users/{userId}.
type PatchUserOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// --- Workspaces ---

// AdminWorkspace is the public DTO for a workspace in the admin panel.
// The Admin prefix avoids the OpenAPI schema collision with the
// member-facing Workspace DTO in the workspace handler package.
type AdminWorkspace struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Description  *string `json:"description,omitempty"`
	IconURL      *string `json:"iconUrl,omitempty"`
	Enabled      bool    `json:"enabled"`
	MemberCount  int64   `json:"memberCount"`
	ProjectCount *int64  `json:"projectCount,omitempty"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    *int64  `json:"updatedAt,omitempty"`
}

// ListWorkspacesInput binds query parameters for GET /admin/workspaces.
type ListWorkspacesInput struct {
	Search  string `query:"search" default:""`
	Enabled string `query:"enabled" default:"" enum:"true,false," doc:"Filter by enabled status (empty=all)"`
	PaginatedInput
}

// ListWorkspacesOutput is the response for GET /admin/workspaces.
type ListWorkspacesOutput struct {
	Body AdminListWorkspacesOutputBody
}

// AdminListWorkspacesOutputBody is the response body for GET /admin/workspaces.
type AdminListWorkspacesOutputBody struct {
	Total int64            `json:"total"`
	Items []AdminWorkspace `json:"items"`
}

// GetWorkspaceInput binds the path parameter for GET /admin/workspaces/{wsId}.
type GetWorkspaceInput struct {
	WsID string `path:"wsId"`
}

// GetWorkspaceOutput is the response for GET /admin/workspaces/{wsId}.
type GetWorkspaceOutput struct {
	Body AdminWorkspace
}

// PatchWorkspaceInput binds the path and body for PATCH /admin/workspaces/{wsId}.
type PatchWorkspaceInput struct {
	WsID string `path:"wsId"`
	Body AdminPatchWorkspaceInputBody
}

// AdminPatchWorkspaceInputBody is the JSON body for PATCH /admin/workspaces/{wsId}.
type AdminPatchWorkspaceInputBody struct {
	Enabled *bool `json:"enabled"`
}

// PatchWorkspaceOutput is the response for PATCH /admin/workspaces/{wsId}.
type PatchWorkspaceOutput struct {
	Body PatchWorkspaceOutputBody
}

// PatchWorkspaceOutputBody is the response body for PATCH /admin/workspaces/{wsId}.
type PatchWorkspaceOutputBody struct {
	Ok bool `json:"ok"`
}

// --- Sessions ---

// Session is the public DTO for a session in the admin panel.
type Session struct {
	ID         string `json:"id"`
	UserAgent  string `json:"userAgent"`
	IPAddress  string `json:"ipAddress"`
	ExpiresAt  int64  `json:"expiresAt"`
	LastUsedAt *int64 `json:"lastUsedAt,omitempty"`
	Active     bool   `json:"active"`
	CreatedAt  int64  `json:"createdAt"`
}

// ListUserSessionsInput binds the path and query parameters for
// GET /admin/users/{userId}/sessions.
type ListUserSessionsInput struct {
	UserID string `path:"userId"`
	PaginatedInput
}

// ListUserSessionsOutput is the response for GET /admin/users/{userId}/sessions.
type ListUserSessionsOutput struct {
	Body struct {
		Total int64     `json:"total"`
		Items []Session `json:"items"`
	}
}

// RevokeSessionInput binds the path parameter for DELETE /admin/sessions/{sessionId}.
type RevokeSessionInput struct {
	SessionID string `path:"sessionId"`
}

// RevokeSessionOutputBody is the response body for DELETE /admin/sessions/{sessionId}.
type RevokeSessionOutputBody struct {
	Ok bool `json:"ok"`
}

// RevokeSessionOutput is the response for DELETE /admin/sessions/{sessionId}.
type RevokeSessionOutput struct {
	Body RevokeSessionOutputBody
}

// --- Instance Admins ---

// InstanceAdmin is the public DTO for an instance admin grant.
type InstanceAdmin struct {
	ID                   string  `json:"id"`
	UserID               string  `json:"userId"`
	Email                string  `json:"email"`
	DisplayName          string  `json:"displayName"`
	AvatarURL            *string `json:"avatarUrl,omitempty"`
	GrantedAt            int64   `json:"grantedAt"`
	GrantedByID          *string `json:"grantedById,omitempty"`
	GrantedByDisplayName *string `json:"grantedByDisplayName,omitempty"`
}

// ListAdminsInput binds query parameters for GET /admin/instance-admins.
type ListAdminsInput struct {
	PaginatedInput
}

// ListAdminsOutput is the response for GET /admin/instance-admins.
type ListAdminsOutput struct {
	Body struct {
		Total int64           `json:"total"`
		Items []InstanceAdmin `json:"items"`
	}
}

// GrantAdminInput binds the body for POST /admin/instance-admins.
type GrantAdminInput struct {
	Body struct {
		UserID string `json:"userId" doc:"User public id (UUID v7)"`
	}
}

// GrantAdminOutput is the response for POST /admin/instance-admins.
type GrantAdminOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// RevokeAdminInput binds the path parameter for DELETE /admin/instance-admins/{userId}.
type RevokeAdminInput struct {
	UserID string `path:"userId" doc:"User public id (UUID v7)"`
}

// RevokeAdminOutput is the response for DELETE /admin/instance-admins/{userId}.
type RevokeAdminOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// --- Audit Logs ---

// AuditEntry is the public DTO for an instance audit log entry.
type AuditEntry struct {
	ID                     string          `json:"id"`
	ActorUserID            *string         `json:"actorUserId,omitempty"`
	ActorDisplayName       *string         `json:"actorDisplayName,omitempty"`
	Action                 string          `json:"action"`
	TargetWorkspaceID      *string         `json:"targetWorkspaceId,omitempty"`
	TargetWorkspaceName    *string         `json:"targetWorkspaceName,omitempty"`
	TargetResourceType     *string         `json:"targetResourceType,omitempty"`
	TargetResourcePublicID *string         `json:"targetResourcePublicId,omitempty"`
	IPAddress              *string         `json:"ipAddress,omitempty"`
	UserAgent              *string         `json:"userAgent,omitempty"`
	Payload                json.RawMessage `json:"payload,omitempty"`
	OccurredAt             int64           `json:"occurredAt"`
}

// ListAuditLogsInput binds query parameters for GET /admin/audit-logs.
type ListAuditLogsInput struct {
	Action string `query:"action" default:""`
	From   int64  `query:"from" default:"0" doc:"Unix seconds, inclusive (0=no lower bound)"`
	To     int64  `query:"to" default:"0" doc:"Unix seconds, inclusive (0=no upper bound)"`
	PaginatedInput
}

// ListAuditLogsOutput is the response for GET /admin/audit-logs.
type ListAuditLogsOutput struct {
	Body struct {
		Total int64        `json:"total"`
		Items []AuditEntry `json:"items"`
	}
}

// --- Settings ---

// InstanceSetting is the public DTO for an instance setting.
type InstanceSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt *int64 `json:"updatedAt,omitempty"`
}

// ListSettingsOutput is the response for GET /admin/settings.
type ListSettingsOutput struct {
	Body struct {
		Items []InstanceSetting `json:"items"`
	}
}

// PatchSettingsInput binds the body for PATCH /admin/settings.
type PatchSettingsInput struct {
	Body struct {
		Settings map[string]string `json:"settings" doc:"Key-value pairs to upsert"`
	}
}

// PatchSettingsOutput is the response for PATCH /admin/settings.
type PatchSettingsOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// --- Instance Stats ---

// InstanceStatsOutput is the response for GET /admin/instance-stats.
type InstanceStatsOutput struct {
	Body struct {
		TotalUsers      int64 `json:"totalUsers"`
		TotalWorkspaces int64 `json:"totalWorkspaces"`
	}
}

// --- Setup ---

// SetupOutput is the response for POST /admin/setup.
type SetupOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}
