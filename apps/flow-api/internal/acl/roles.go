// Package acl is the single source of truth for nodate-flow access
// control checks. It contains the four-layer role hierarchy
// (instance / workspace / project / task visibility) and the pure
// decision functions that enforce it.
//
// The HTTP middleware (apps/flow-api/internal/http/middleware/acl.go)
// and the MCP tool layer (apps/flow-api/internal/mcp/acl.go) both call
// into this package so the rules cannot drift between transports. Each
// transport keeps its own thin wrapper that performs the request /
// session specific extraction and then delegates to acl.Check*.
//
// This package is flow-api-specific (it talks to flow-api's schema and
// error specs) so it lives under apps/flow-api/internal/ rather than
// packages/go-shared.
package acl

// WorkspaceRole is the role of a user inside a workspace. The hierarchy
// is owner > admin > member > guest.
type WorkspaceRole string

// Workspace role constants. Order matters for [WorkspaceRole.AtLeast].
const (
	WorkspaceRoleGuest  WorkspaceRole = "guest"
	WorkspaceRoleMember WorkspaceRole = "member"
	WorkspaceRoleAdmin  WorkspaceRole = "admin"
	WorkspaceRoleOwner  WorkspaceRole = "owner"
)

var workspaceRoleRank = map[WorkspaceRole]int{
	WorkspaceRoleGuest:  1,
	WorkspaceRoleMember: 2,
	WorkspaceRoleAdmin:  3,
	WorkspaceRoleOwner:  4,
}

// AtLeast reports whether the receiver role meets or exceeds the given
// minimum role in the workspace hierarchy.
func (r WorkspaceRole) AtLeast(minRole WorkspaceRole) bool {
	return workspaceRoleRank[r] >= workspaceRoleRank[minRole]
}

// IsValid reports whether r is one of the four known workspace role
// constants. Used by the middleware to reject DB rows whose role
// column drifted out of the closed enum (typically a manual edit or
// a schema migration that fell behind). An invalid role surfaces as
// INTERNAL.UNEXPECTED, never as a permissive "default" role.
func (r WorkspaceRole) IsValid() bool {
	_, ok := workspaceRoleRank[r]
	return ok
}

// ProjectRole is the role of a user inside a project. The hierarchy is
// lead > editor > commenter > viewer.
type ProjectRole string

// Project role constants. Order matters for [ProjectRole.AtLeast].
const (
	// ProjectRoleElevated indicates that the caller has elevated workspace-level
	// access (owner or admin) and is not scoped to a specific project role.
	ProjectRoleElevated  ProjectRole = ""
	ProjectRoleViewer    ProjectRole = "viewer"
	ProjectRoleCommenter ProjectRole = "commenter"
	ProjectRoleEditor    ProjectRole = "editor"
	ProjectRoleLead      ProjectRole = "lead"
)

var projectRoleRank = map[ProjectRole]int{
	ProjectRoleViewer:    1,
	ProjectRoleCommenter: 2,
	ProjectRoleEditor:    3,
	ProjectRoleLead:      4,
}

// AtLeast reports whether the receiver role meets or exceeds the given
// minimum role in the project hierarchy.
func (r ProjectRole) AtLeast(minRole ProjectRole) bool {
	return projectRoleRank[r] >= projectRoleRank[minRole]
}

// IsValid reports whether r is one of the known project role
// constants. The empty string ([ProjectRoleElevated]) counts as
// valid because it is the explicit marker for workspace-level
// elevation. Any other unknown value is treated as a server-side
// data invariant violation by the caller.
func (r ProjectRole) IsValid() bool {
	if r == ProjectRoleElevated {
		return true
	}
	_, ok := projectRoleRank[r]
	return ok
}

// TaskVisibility represents the Layer 4 task-level visibility setting.
type TaskVisibility string

// Task visibility constants.
const (
	// TaskVisibilityPublic means any workspace member can see the task.
	TaskVisibilityPublic TaskVisibility = "public"
	// TaskVisibilityProject means only members of the task's parent project
	// (or workspace admins/owners) can see the task.
	TaskVisibilityProject TaskVisibility = "project"
	// TaskVisibilityPrivate means only users who are actors on the task
	// (assignee, reviewer, watcher, approver, or creator) can see it.
	// Workspace admins/owners are also granted access.
	TaskVisibilityPrivate TaskVisibility = "private"
)
