// Package middleware contains chi-compatible HTTP middlewares for the
// nodate-flow API, including authentication and ACL (access control list)
// enforcement. ACL is intentionally implemented as middleware so that route
// handlers never perform ad-hoc permission checks (see @acl agent rules).
package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Error code locals re-exported from the generated errors package so the
// rest of this file can keep its original symbol names.
var (
	errCodeInstanceAdminRequired = apierrors.InstanceAdminRequired.Code
	errCodeWorkspaceNotFound     = apierrors.WsWorkspaceNotFound.Code
	errCodeWorkspaceAccessDenied = apierrors.WsWorkspaceAccessDenied.Code
	errCodeProjectNotFound       = apierrors.WsProjectNotFound.Code
	errCodeProjectAccessDenied   = apierrors.WsProjectAccessDenied.Code
	errCodeMemberRoleDenied      = apierrors.WsMemberRoleDenied.Code
	errCodeTaskNotFound          = apierrors.WsTaskNotFound.Code
	errCodeTaskAccessDenied      = apierrors.WsTaskAccessDenied.Code
)

// ----------------------------------------------------------------------------
// Roles
// ----------------------------------------------------------------------------

// WorkspaceRole is the role of a user inside a workspace. The hierarchy is
// owner > admin > member > guest.
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
func (r WorkspaceRole) AtLeast(min WorkspaceRole) bool {
	return workspaceRoleRank[r] >= workspaceRoleRank[min]
}

// ProjectRole is the role of a user inside a project. The hierarchy is
// lead > editor > commenter > viewer.
type ProjectRole string

// Project role constants. Order matters for [ProjectRole.AtLeast].
const (
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
func (r ProjectRole) AtLeast(min ProjectRole) bool {
	return projectRoleRank[r] >= projectRoleRank[min]
}

// ----------------------------------------------------------------------------
// Context plumbing
// ----------------------------------------------------------------------------

// ctxKey is an unexported type used as a context key to avoid collisions with
// other packages stuffing values into the request context.
type ctxKey int

const (
	ctxKeyActorUserID ctxKey = iota
	ctxKeyWorkspaceID
	ctxKeyProjectID
	ctxKeyWorkspaceRole
	ctxKeyProjectRole
	ctxKeyWorkspaceIDPublic
	ctxKeyProjectIDPublic
	ctxKeyTaskID
	ctxKeyTaskIDPublic
	ctxKeySessionIDPublic
	ctxKeyClientIP
)

// TaskContext is the task metadata injected by [RequireTaskAccess]. It is
// scoped to a single request and never exposed externally.
type TaskContext struct {
	// ID is the internal task id (never sent over the wire).
	ID uint32
	// PublicID is the task UUID v7.
	PublicID uuid.UUID
}

// TaskFromContext extracts the task metadata established by
// [RequireTaskAccess]. The boolean is false when the middleware did not
// run on the request path.
func TaskFromContext(ctx context.Context) (TaskContext, bool) {
	id, ok := ctx.Value(ctxKeyTaskID).(uint32)
	if !ok {
		return TaskContext{}, false
	}
	pub, _ := ctx.Value(ctxKeyTaskIDPublic).(uuid.UUID)
	return TaskContext{ID: id, PublicID: pub}, true
}

// WithActor returns a new context carrying the authenticated user's internal
// numeric id. This is normally called by the auth middleware before any ACL
// middleware runs.
func WithActor(ctx context.Context, userID uint32) context.Context {
	return context.WithValue(ctx, ctxKeyActorUserID, userID)
}

// ActorFromContext extracts the authenticated user's internal id. The boolean
// is false when no auth middleware has populated the context.
func ActorFromContext(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(ctxKeyActorUserID).(uint32)
	return v, ok
}

// WithSessionPublicID returns a new context carrying the caller's session
// public id, resolved from the "sid" claim on the access token.
func WithSessionPublicID(ctx context.Context, sid types.PublicID) context.Context {
	return context.WithValue(ctx, ctxKeySessionIDPublic, sid)
}

// SessionPublicIDFromContext extracts the caller's session public id
// as populated by [RequireAuth]. The boolean is false when no session
// id was present on the access token (e.g. legacy PAT/MCP tokens).
func SessionPublicIDFromContext(ctx context.Context) (types.PublicID, bool) {
	v, ok := ctx.Value(ctxKeySessionIDPublic).(types.PublicID)
	if !ok {
		return types.PublicID{}, false
	}
	var zero types.PublicID
	if v == zero {
		return zero, false
	}
	return v, true
}

// WithClientIP returns a new context carrying the caller's client IP
// address (already normalized by [ClientIP]).
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP, ip)
}

// ClientIPFromContext extracts the caller's client IP address as
// populated by [ClientIP]. Returns an empty string when no middleware
// has populated the context.
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

// WorkspaceContext is the workspace metadata injected by
// [RequireWorkspaceMember] for downstream handlers.
type WorkspaceContext struct {
	// ID is the internal workspace id (never exposed externally).
	ID uint32
	// PublicID is the workspace UUID v7.
	PublicID uuid.UUID
	// Role is the actor's role inside this workspace.
	Role WorkspaceRole
}

// WorkspaceFromContext extracts the workspace metadata established by
// [RequireWorkspaceMember]. The boolean is false when the middleware did not
// run on this request path.
func WorkspaceFromContext(ctx context.Context) (WorkspaceContext, bool) {
	id, ok := ctx.Value(ctxKeyWorkspaceID).(uint32)
	if !ok {
		return WorkspaceContext{}, false
	}
	role, _ := ctx.Value(ctxKeyWorkspaceRole).(WorkspaceRole)
	pub, _ := ctx.Value(ctxKeyWorkspaceIDPublic).(uuid.UUID)
	return WorkspaceContext{ID: id, PublicID: pub, Role: role}, true
}

// ProjectContext is the project metadata injected by [RequireProjectMember].
type ProjectContext struct {
	// ID is the internal project id (never exposed externally).
	ID uint32
	// PublicID is the project UUID v7.
	PublicID uuid.UUID
	// Role is the actor's role inside this project. Empty when access is
	// granted purely via workspace admin/owner elevation.
	Role ProjectRole
}

// ProjectFromContext extracts the project metadata established by
// [RequireProjectMember].
func ProjectFromContext(ctx context.Context) (ProjectContext, bool) {
	id, ok := ctx.Value(ctxKeyProjectID).(uint32)
	if !ok {
		return ProjectContext{}, false
	}
	role, _ := ctx.Value(ctxKeyProjectRole).(ProjectRole)
	pub, _ := ctx.Value(ctxKeyProjectIDPublic).(uuid.UUID)
	return ProjectContext{ID: id, PublicID: pub, Role: role}, true
}

// ----------------------------------------------------------------------------
// Error response
// ----------------------------------------------------------------------------

// errorBody mirrors the wire shape of the canonical ErrorResponse DTO. It is
// duplicated here to avoid an import cycle with the handlers package; the
// real type lives under apps/api/internal/http/handlers and is generated from
// the OpenAPI definition.
//
// TODO(1.OPENAPI-1): Replace with a shared dto package once it exists.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a JSON error response. ACL middleware cannot return
// errors via Huma's pipeline (it sits at the chi layer above Huma), so we
// emit the same shape directly.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}

// ----------------------------------------------------------------------------
// Database surface
// ----------------------------------------------------------------------------

// ACLDB is the minimal subset of *sql.DB that the ACL middleware needs.
// Defining it as an interface keeps the middleware testable without a live
// database connection.
type ACLDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ----------------------------------------------------------------------------
// Instance-level
// ----------------------------------------------------------------------------

// RequireInstanceAdmin returns a middleware that allows the request through
// only when the actor has an active row in instance_admins. It assumes an
// auth middleware has already populated the actor via [WithActor].
//
// On failure it responds 403 INSTANCE.ADMIN.REQUIRED.
func RequireInstanceAdmin(db ACLDB) func(http.Handler) http.Handler {
	const q = `SELECT 1 FROM instance_admins
WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeInstanceAdminRequired,
					"Instance administrator privileges are required")
				return
			}
			var one int
			err := db.QueryRowContext(r.Context(), q, userID).Scan(&one)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden, errCodeInstanceAdminRequired,
						"Instance administrator privileges are required")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED",
					"Internal error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ----------------------------------------------------------------------------
// Workspace-level
// ----------------------------------------------------------------------------

// RequireWorkspaceMember returns a middleware that resolves the workspace
// from the {wsId} path parameter (a UUID v7), verifies that the actor is an
// enabled member, and stores the internal workspace id and role in the
// request context.
//
// On failure it responds:
//   - 404 WS.WORKSPACE.NOT_FOUND when the workspace does not exist or the
//     path parameter is not a valid UUID.
//   - 403 WS.WORKSPACE.ACCESS_DENIED when the actor is not a member.
func RequireWorkspaceMember(db ACLDB) func(http.Handler) http.Handler {
	const wsQuery = `SELECT id FROM workspaces
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const memQuery = `SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeWorkspaceAccessDenied,
					"You do not have access to this workspace")
				return
			}
			raw := chi.URLParam(r, "wsId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, errCodeWorkspaceNotFound,
					"Workspace not found")
				return
			}
			var wsID uint32
			if err := db.QueryRowContext(r.Context(), wsQuery, types.FromUUID(pub)).Scan(&wsID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeWorkspaceNotFound,
						"Workspace not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var role string
			if err := db.QueryRowContext(r.Context(), memQuery, wsID, userID).Scan(&role); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden, errCodeWorkspaceAccessDenied,
						"You do not have access to this workspace")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, wsID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRole(role))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireWorkspaceRole returns a middleware that asserts the actor's
// workspace role (previously injected by [RequireWorkspaceMember]) meets the
// given minimum. Responds 403 WS.MEMBER.ROLE_DENIED on failure.
func RequireWorkspaceRole(min WorkspaceRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, ok := WorkspaceFromContext(r.Context())
			if !ok || !ws.Role.AtLeast(min) {
				writeError(w, http.StatusForbidden, errCodeMemberRoleDenied,
					"Your role does not permit this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ----------------------------------------------------------------------------
// Project-level
// ----------------------------------------------------------------------------

// RequireProjectMember returns a middleware that resolves the project from
// the {prjId} path parameter, verifies the project belongs to the
// already-resolved workspace, and grants access when either:
//
//  1. The actor has an enabled row in project_members for that project, or
//  2. The actor is a workspace owner or admin (elevated access).
//
// It must be chained after [RequireWorkspaceMember]. On failure it responds:
//   - 404 WS.PROJECT.NOT_FOUND when the project does not exist in the
//     workspace or the path param is not a valid UUID.
//   - 403 WS.PROJECT.ACCESS_DENIED otherwise.
//
// Layer 4 task visibility (public/project/private) is enforced by
// [RequireTaskAccess] (single-task endpoints) and [TaskVisibilityFilter]
// (list endpoints). This middleware only covers instance/workspace/project
// layers.
func RequireProjectMember(db ACLDB) func(http.Handler) http.Handler {
	const prjQuery = `SELECT id FROM projects
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	const memQuery = `SELECT role FROM project_members
WHERE workspace_id = ? AND project_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
					"You do not have access to this project")
				return
			}
			ws, ok := WorkspaceFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusNotFound, errCodeProjectNotFound,
					"Project not found")
				return
			}
			raw := chi.URLParam(r, "prjId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, errCodeProjectNotFound,
					"Project not found")
				return
			}
			var prjID uint32
			if err := db.QueryRowContext(r.Context(), prjQuery, ws.ID, types.FromUUID(pub)).Scan(&prjID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeProjectNotFound,
						"Project not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var role ProjectRole
			var roleStr string
			err = db.QueryRowContext(r.Context(), memQuery, ws.ID, prjID, userID).Scan(&roleStr)
			switch {
			case err == nil:
				role = ProjectRole(roleStr)
			case errors.Is(err, sql.ErrNoRows):
				// Workspace owners and admins can act on every project.
				if !ws.Role.AtLeast(WorkspaceRoleAdmin) {
					writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
						"You do not have access to this project")
					return
				}
				// Elevated access: leave role empty so handlers can decide.
				role = ""
			default:
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyProjectID, prjID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyProjectRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireProjectMemberByGlobalId returns a middleware that resolves a project
// from the {prjId} path parameter alone (no {wsId} in the URL). It looks up
// the project globally by public_id, derives the owning workspace, verifies
// workspace membership, then applies the same project membership / workspace
// elevation logic as [RequireProjectMember]. Both workspace and project
// contexts are injected for downstream handlers.
//
// TODO(@query): Replace the inline SELECTs with a generated
// FindProjectByPublicIdGlobal query once available.
func RequireProjectMemberByGlobalId(db ACLDB) func(http.Handler) http.Handler {
	const prjQuery = `SELECT id, workspace_id FROM projects
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const wsQuery = `SELECT public_id FROM workspaces
WHERE id = ? AND enabled = TRUE LIMIT 1`
	const wsMemQuery = `SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	const prjMemQuery = `SELECT role FROM project_members
WHERE workspace_id = ? AND project_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
					"You do not have access to this project")
				return
			}
			raw := chi.URLParam(r, "prjId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, errCodeProjectNotFound,
					"Project not found")
				return
			}
			var prjID, wsID uint32
			if err := db.QueryRowContext(r.Context(), prjQuery, types.FromUUID(pub)).Scan(&prjID, &wsID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeProjectNotFound,
						"Project not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var wsPubID types.PublicID
			if err := db.QueryRowContext(r.Context(), wsQuery, wsID).Scan(&wsPubID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeProjectNotFound,
						"Project not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var wsRoleStr string
			if err := db.QueryRowContext(r.Context(), wsMemQuery, wsID, userID).Scan(&wsRoleStr); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
						"You do not have access to this project")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			wsRole := WorkspaceRole(wsRoleStr)
			var role ProjectRole
			var roleStr string
			err = db.QueryRowContext(r.Context(), prjMemQuery, wsID, prjID, userID).Scan(&roleStr)
			switch {
			case err == nil:
				role = ProjectRole(roleStr)
			case errors.Is(err, sql.ErrNoRows):
				if !wsRole.AtLeast(WorkspaceRoleAdmin) {
					writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
						"You do not have access to this project")
					return
				}
				role = ""
			default:
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, wsID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, wsPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, wsRole)
			ctx = context.WithValue(ctx, ctxKeyProjectID, prjID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyProjectRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

// RequireTaskAccess returns a middleware that resolves the task from the
// {id} path parameter (a UUID v7), verifies the actor has access to the
// owning workspace and project (with workspace owner/admin elevation),
// enforces Layer 4 task visibility (public/project/private), and injects
// the task internal id into the request context via [TaskFromContext].
// It also injects workspace and project context for downstream handlers.
//
// Visibility rules (Layer 4):
//   - public:  any workspace member can access the task.
//   - project: the actor must be a project member (or ws admin/owner).
//   - private: the actor must be a task actor (assignee, reviewer, watcher,
//     approver) or the task creator. Workspace admins/owners bypass this.
//
// On failure it responds:
//   - 404 WS.TASK.NOT_FOUND when the task does not exist or the path
//     parameter is not a valid UUID.
//   - 403 WS.TASK.ACCESS_DENIED when the actor cannot access the task's
//     project / workspace, or when task visibility denies access.
func RequireTaskAccess(db ACLDB) func(http.Handler) http.Handler {
	const taskQuery = `SELECT id, workspace_id, project_id, visibility, created_by_user_id FROM tasks
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const wsQuery = `SELECT public_id FROM workspaces
WHERE id = ? AND enabled = TRUE LIMIT 1`
	const wsMemQuery = `SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	const prjQuery = `SELECT public_id FROM projects
WHERE id = ? AND enabled = TRUE LIMIT 1`
	const prjMemQuery = `SELECT role FROM project_members
WHERE workspace_id = ? AND project_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	const taskActorQuery = `SELECT 1 FROM task_actors
WHERE task_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeTaskAccessDenied,
					"You do not have access to this task")
				return
			}
			raw := chi.URLParam(r, "id")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
				return
			}
			var taskID, wsID, prjID uint32
			var visibility string
			var createdByUserID sql.NullInt32
			if err := db.QueryRowContext(r.Context(), taskQuery, types.FromUUID(pub)).Scan(&taskID, &wsID, &prjID, &visibility, &createdByUserID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var wsPubID types.PublicID
			if err := db.QueryRowContext(r.Context(), wsQuery, wsID).Scan(&wsPubID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var wsRoleStr string
			if err := db.QueryRowContext(r.Context(), wsMemQuery, wsID, userID).Scan(&wsRoleStr); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden, errCodeTaskAccessDenied,
						"You do not have access to this task")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			wsRole := WorkspaceRole(wsRoleStr)

			// Layer 3: project membership check.
			var prjPubID types.PublicID
			if err := db.QueryRowContext(r.Context(), prjQuery, prjID).Scan(&prjPubID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var prjRole ProjectRole
			var prjRoleStr string
			isProjectMember := false
			err = db.QueryRowContext(r.Context(), prjMemQuery, wsID, prjID, userID).Scan(&prjRoleStr)
			switch {
			case err == nil:
				prjRole = ProjectRole(prjRoleStr)
				isProjectMember = true
			case errors.Is(err, sql.ErrNoRows):
				if !wsRole.AtLeast(WorkspaceRoleAdmin) {
					writeError(w, http.StatusForbidden, errCodeTaskAccessDenied,
						"You do not have access to this task")
					return
				}
				prjRole = ""
			default:
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}

			// Layer 4: task visibility enforcement.
			isElevated := wsRole.AtLeast(WorkspaceRoleAdmin)
			switch TaskVisibility(visibility) {
			case TaskVisibilityPublic:
				// Any workspace member can access -- already verified above.
			case TaskVisibilityProject:
				// Requires project membership or workspace admin/owner elevation.
				if !isProjectMember && !isElevated {
					// Return 404 to avoid leaking existence of private tasks.
					writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
					return
				}
			case TaskVisibilityPrivate:
				// Requires being a task actor (or creator), unless ws admin/owner.
				if !isElevated {
					isCreator := createdByUserID.Valid && uint32(createdByUserID.Int32) == userID
					if !isCreator {
						var one int
						actorErr := db.QueryRowContext(r.Context(), taskActorQuery, taskID, userID).Scan(&one)
						if actorErr != nil {
							// Return 404 to avoid leaking existence.
							writeError(w, http.StatusNotFound, errCodeTaskNotFound, "Task not found")
							return
						}
					}
				}
			}

			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, wsID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, wsPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, wsRole)
			ctx = context.WithValue(ctx, ctxKeyProjectID, prjID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, prjPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyProjectRole, prjRole)
			ctx = context.WithValue(ctx, ctxKeyTaskID, taskID)
			ctx = context.WithValue(ctx, ctxKeyTaskIDPublic, pub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TaskVisibilityFilter returns a SQL WHERE fragment and associated bind
// arguments that enforce Layer 4 task visibility in list queries. The
// fragment references v_task_list columns and should be ANDed into an
// existing WHERE clause.
//
// The userID is the actor's internal id. wsRole is the actor's workspace
// role (from context). When the actor is a workspace admin or owner, no
// additional filtering is applied (all tasks are visible regardless of
// visibility setting).
//
// The returned fragment uses the v_task_list aliases:
//   - v.visibility, v.project_id, v.task_internal_id, v.created_by_user_id
func TaskVisibilityFilter(userID uint32, wsRole WorkspaceRole) (fragment string, args []any) {
	if wsRole.AtLeast(WorkspaceRoleAdmin) {
		// Admins/owners see everything.
		return "", nil
	}
	// For non-elevated users, filter out tasks they cannot see:
	// - public: always visible (workspace membership already checked)
	// - project: visible if user is a project member
	// - private: visible if user is a task actor or creator
	const frag = `(
    v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm
      WHERE pm.project_id = v.project_id
        AND pm.user_id = ?
        AND pm.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = ?
      OR EXISTS (
        SELECT 1 FROM task_actors ta
        WHERE ta.task_id = v.task_internal_id
          AND ta.user_id = ?
          AND ta.enabled = TRUE
      )
    ))
  )`
	return frag, []any{userID, userID, userID}
}

// RequireProjectRole returns a middleware that asserts the actor's project
// role (previously injected by [RequireProjectMember]) meets the given
// minimum. Workspace owners and admins always pass even with an empty
// project role.
//
// Responds 403 WS.PROJECT.ACCESS_DENIED on failure.
func RequireProjectRole(min ProjectRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prj, ok := ProjectFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
					"You do not have access to this project")
				return
			}
			if prj.Role == "" {
				// Elevated workspace access established by RequireProjectMember.
				next.ServeHTTP(w, r)
				return
			}
			if !prj.Role.AtLeast(min) {
				writeError(w, http.StatusForbidden, errCodeProjectAccessDenied,
					"You do not have access to this project")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
