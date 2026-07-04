// Package middleware contains chi-compatible HTTP middlewares for the
// nodate-flow API, including authentication and ACL (access control list)
// enforcement. ACL is intentionally implemented as middleware so that route
// handlers never perform ad-hoc permission checks (see @acl agent rules).
//
// The actual access decisions live in
// apps/flow-api/internal/acl. This file is a thin chi adapter that
// extracts URL params + actor from the request, delegates to the
// shared package, and translates returned [apierrors.APIError] values
// into JSON error responses.
package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/acl"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	sharedacl "github.com/nodate-flow/nodate-flow/packages/go-shared/acl"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ----------------------------------------------------------------------------
// Roles -- re-exported aliases for backward compatibility with handlers
// that imported these names from this package before the acl/ refactor.
// ----------------------------------------------------------------------------

// WorkspaceRole aliases [acl.WorkspaceRole].
type WorkspaceRole = acl.WorkspaceRole

// Workspace role constants. These mirror [acl] for callers that
// historically reached for the middleware package.
const (
	WorkspaceRoleGuest  = acl.WorkspaceRoleGuest
	WorkspaceRoleMember = acl.WorkspaceRoleMember
	WorkspaceRoleAdmin  = acl.WorkspaceRoleAdmin
	WorkspaceRoleOwner  = acl.WorkspaceRoleOwner
)

// ProjectRole aliases [acl.ProjectRole].
type ProjectRole = acl.ProjectRole

// Project role constants.
const (
	ProjectRoleElevated  = acl.ProjectRoleElevated
	ProjectRoleViewer    = acl.ProjectRoleViewer
	ProjectRoleCommenter = acl.ProjectRoleCommenter
	ProjectRoleEditor    = acl.ProjectRoleEditor
	ProjectRoleLead      = acl.ProjectRoleLead
)

// TaskVisibility aliases [acl.TaskVisibility].
type TaskVisibility = acl.TaskVisibility

// Task visibility constants.
const (
	TaskVisibilityPublic  = acl.TaskVisibilityPublic
	TaskVisibilityProject = acl.TaskVisibilityProject
	TaskVisibilityPrivate = acl.TaskVisibilityPrivate
)

// ----------------------------------------------------------------------------
// Context plumbing
// ----------------------------------------------------------------------------

// ctxKey is an unexported type used as a context key for ACL-specific
// values (workspace, project, task). Authentication-level keys
// (actor, session, client IP) are managed by the shared authn package.
type ctxKey int

const (
	ctxKeyWorkspaceID ctxKey = iota
	ctxKeyProjectID
	ctxKeyWorkspaceRole
	ctxKeyProjectRole
	ctxKeyWorkspaceIDPublic
	ctxKeyProjectIDPublic
	ctxKeyTaskID
	ctxKeyTaskIDPublic
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

// WithActor delegates to [authn.WithActor].
func WithActor(ctx context.Context, userID uint32) context.Context {
	return authn.WithActor(ctx, userID)
}

// ActorFromContext delegates to [authn.ActorFromContext].
func ActorFromContext(ctx context.Context) (uint32, bool) {
	return authn.ActorFromContext(ctx)
}

// WithSessionPublicID delegates to [authn.WithSessionPublicID].
func WithSessionPublicID(ctx context.Context, sid types.PublicID) context.Context {
	return authn.WithSessionPublicID(ctx, sid)
}

// SessionPublicIDFromContext delegates to [authn.SessionPublicIDFromContext].
func SessionPublicIDFromContext(ctx context.Context) (types.PublicID, bool) {
	return authn.SessionPublicIDFromContext(ctx)
}

// WithClientIP delegates to [authn.WithClientIP].
func WithClientIP(ctx context.Context, ip string) context.Context {
	return authn.WithClientIP(ctx, ip)
}

// ClientIPFromContext delegates to [authn.ClientIPFromContext].
func ClientIPFromContext(ctx context.Context) string {
	return authn.ClientIPFromContext(ctx)
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
//
// The role string is validated against the closed [ProjectRole] enum.
// If a corrupted DB row injects an unknown value into the context,
// callers receive ok=false here and a generic 500 INTERNAL.UNEXPECTED
// upstream rather than a misleading 403 — corrupt enum data is a
// server-side invariant violation, not a permissions failure on the
// caller's part. Validation at the read site is defence in depth: the
// injecting middleware ([RequireProjectMember],
// [RequireProjectMemberByGlobalID], [RequireTaskAccess]) already rejects
// invalid roles before they reach the context.
func ProjectFromContext(ctx context.Context) (ProjectContext, bool) {
	id, ok := ctx.Value(ctxKeyProjectID).(uint32)
	if !ok {
		return ProjectContext{}, false
	}
	role, _ := ctx.Value(ctxKeyProjectRole).(ProjectRole)
	if !role.IsValid() {
		return ProjectContext{}, false
	}
	pub, _ := ctx.Value(ctxKeyProjectIDPublic).(uuid.UUID)
	return ProjectContext{ID: id, PublicID: pub, Role: role}, true
}

// ----------------------------------------------------------------------------
// Error response
// ----------------------------------------------------------------------------

// problemBody is the RFC 9457 problem+json wire shape emitted by the
// chi-level middlewares (ACL, calendar). The struct is duplicated here
// rather than re-using [handlerutil.WriteSpecError] because that
// package depends on this `middleware` package for [ActorFromContext]
// — importing it back from middleware would create a cycle. The
// fields mirror handlerutil's struct verbatim so the wire payload is
// byte-identical regardless of which layer emitted the error.
type problemBody struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Status      int    `json:"status"`
	Detail      string `json:"detail"`
	Description string `json:"description,omitempty"`
	UserAction  string `json:"userAction,omitempty"`
}

// writeSpecError writes the canonical RFC 9457 problem+json error
// envelope for the chi-level middlewares (ACL, calendar). They sit
// above Huma's pipeline so they cannot return a `huma.StatusError`;
// the body shape mirrors handlerutil.WriteSpecError so clients can
// branch on the same `type` field regardless of which layer emitted
// the error.
func writeSpecError(w http.ResponseWriter, spec *apierrors.Spec) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(spec.Status)
	_ = json.NewEncoder(w).Encode(problemBody{
		Type:        spec.Code,
		Title:       http.StatusText(spec.Status),
		Status:      spec.Status,
		Detail:      spec.Message,
		Description: spec.Description,
		UserAction:  spec.UserAction,
	})
}

// writeAPIError converts an *apierrors.APIError into a JSON error
// response using the spec's status code. Non-APIError values are
// written as a generic 500 INTERNAL.UNEXPECTED to avoid leaking
// internal error strings.
func writeAPIError(w http.ResponseWriter, err error) {
	var ae *apierrors.APIError
	if stderrors.As(err, &ae) && ae.Spec != nil {
		writeSpecError(w, ae.Spec)
		return
	}
	writeSpecError(w, apierrors.InternalUnexpected)
}

// hasSpec reports whether err is an APIError carrying the given spec.
func hasSpec(err error, spec *apierrors.Spec) bool {
	var ae *apierrors.APIError
	return stderrors.As(err, &ae) && ae.Spec == spec
}

// ----------------------------------------------------------------------------
// Database surface
// ----------------------------------------------------------------------------

// ACLDB is the minimal subset of *sql.DB that the ACL middleware needs.
// Defining it as an interface keeps the middleware testable without a
// live database connection. It is identical to [acl.DB] and accepted by
// the same shared check functions.
type ACLDB = acl.DB

// ----------------------------------------------------------------------------
// Instance-level
// ----------------------------------------------------------------------------

// codeSpec maps a canonical error code string to its [apierrors.Spec]
// pointer. The shared instance-admin middleware
// ([sharedacl.RequireInstanceAdmin]) emits errors as
// `(status, code, message)` tuples; this map lets the local adapter
// recover the rich catalog metadata (Description / UserAction) before
// delegating to [writeSpecError]. Unknown codes fall through to a
// synthetic Spec built from the supplied tuple so the wire shape stays
// RFC 9457-compliant for any future code that might be added to the
// shared package.
var codeSpec = map[string]*apierrors.Spec{
	sharedacl.CodeSessionUnauthorized:   apierrors.AuthSessionUnauthorized,
	sharedacl.CodeInstanceAdminRequired: apierrors.AuthPermissionInstanceAdminRequired,
	sharedacl.CodeInternalUnexpected:    apierrors.InternalUnexpected,
}

// writeSpecErrorByCode renders the RFC 9457 envelope for a (status,
// code, message) tuple emitted by [sharedacl.RequireInstanceAdmin]. It
// prefers the canonical [apierrors.Spec] for the code so the wire
// payload carries the catalog Description / UserAction; for unknown
// codes it builds a synthetic Spec on the fly so the wire shape stays
// stable.
func writeSpecErrorByCode(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	if spec, ok := codeSpec[code]; ok {
		writeSpecError(w, spec)
		return
	}
	writeSpecError(w, &apierrors.Spec{
		Code:    code,
		Status:  status,
		Message: message,
	})
}

// RequireInstanceAdmin returns a middleware that allows the request through
// only when the actor has an active row in instance_admins. The decision
// logic is delegated to [sharedacl.RequireInstanceAdmin] from
// `packages/go-shared/acl/`; this function only wires the app-specific
// callbacks (sqlc query, actor extractor, RFC 9457 error writer).
//
// On failure it responds:
//   - 401 AUTH.SESSION.UNAUTHORIZED when no actor is present on the
//     request context.
//   - 403 AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED when the actor is
//     authenticated but is not an instance administrator.
//   - 500 INTERNAL.UNEXPECTED for transport-level lookup failures.
//
// All responses are emitted via [writeSpecError] so the body is RFC
// 9457 problem+json (type / title / status / detail).
func RequireInstanceAdmin(q *generated.Queries) func(http.Handler) http.Handler {
	return sharedacl.RequireInstanceAdmin(sharedacl.Config{
		IsInstanceAdmin: func(ctx context.Context, uid uint32) (bool, error) {
			_, err := q.AdminFindInstanceAdminByUserId(ctx, uid)
			if stderrors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return true, nil
		},
		ExtractActor: func(r *http.Request) (uint32, bool) {
			return authn.ActorFromContext(r.Context())
		},
		WriteError: writeSpecErrorByCode,
	})
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
//
// Side effect: once the workspace context is in scope, the helper also
// re-runs the slog logger enrichment so [nflog.LoggerFromContext] (called
// by downstream handlers) returns a logger that already carries
// workspace_id / workspace_public_id without each handler having to
// stamp those attrs on every log line.
func RequireWorkspaceMember(db ACLDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsWorkspaceAccessDenied)
				return
			}
			raw := chi.URLParam(r, "wsId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeSpecError(w, apierrors.WsWorkspaceNotFound)
				return
			}
			access, err := acl.ResolveWorkspaceAccess(r.Context(), db, pub, userID)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, access.ID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, access.Role)
			ctx = enrichLoggerWithWorkspace(ctx, access.ID, pub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireWorkspaceRole returns a middleware that asserts the actor's
// workspace role (previously injected by [RequireWorkspaceMember]) meets the
// given minimum. Responds 403 WS.MEMBER.ROLE_DENIED on failure.
func RequireWorkspaceRole(minRole WorkspaceRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, ok := WorkspaceFromContext(r.Context())
			if !ok || !ws.Role.AtLeast(minRole) {
				writeSpecError(w, apierrors.WsMemberRoleDenied)
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsProjectAccessDenied)
				return
			}
			ws, ok := WorkspaceFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsProjectNotFound)
				return
			}
			raw := chi.URLParam(r, "prjId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeSpecError(w, apierrors.WsProjectNotFound)
				return
			}
			prjID, err := acl.ResolveProjectInWorkspace(r.Context(), db, ws.ID, pub)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			role, _, err := acl.CheckProjectMembership(r.Context(), db, ws.ID, prjID, userID, ws.Role, nil)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyProjectID, prjID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyProjectRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireProjectMemberByGlobalID returns a middleware that resolves a project
// from the {prjId} path parameter alone (no {wsId} in the URL). It looks up
// the project globally by public_id, derives the owning workspace, verifies
// workspace membership, then applies the same project membership / workspace
// elevation logic as [RequireProjectMember]. Both workspace and project
// contexts are injected for downstream handlers.
func RequireProjectMemberByGlobalID(db ACLDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsProjectAccessDenied)
				return
			}
			raw := chi.URLParam(r, "prjId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeSpecError(w, apierrors.WsProjectNotFound)
				return
			}
			prj, err := acl.ResolveProjectByPublicID(r.Context(), db, pub)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			wsPubID, err := acl.ResolveWorkspacePublicByID(r.Context(), db, prj.WorkspaceID)
			if err != nil {
				// Workspace-not-found leaks here as project-not-found to
				// match the historical surface of this middleware. Transport
				// errors propagate as INTERNAL.UNEXPECTED via writeAPIError.
				if hasSpec(err, apierrors.WsWorkspaceNotFound) {
					writeSpecError(w, apierrors.WsProjectNotFound)
					return
				}
				writeAPIError(w, err)
				return
			}
			wsRole, err := acl.CheckWorkspaceMember(r.Context(), db, prj.WorkspaceID, userID, apierrors.WsProjectAccessDenied)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			role, _, err := acl.CheckProjectMembership(r.Context(), db, prj.WorkspaceID, prj.ID, userID, wsRole, nil)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, prj.WorkspaceID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, wsPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, wsRole)
			ctx = context.WithValue(ctx, ctxKeyProjectID, prj.ID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyProjectRole, role)
			ctx = enrichLoggerWithWorkspace(ctx, prj.WorkspaceID, wsPubID.UUID())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := ActorFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsTaskAccessDenied)
				return
			}
			raw := chi.URLParam(r, "id")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeSpecError(w, apierrors.WsTaskNotFound)
				return
			}
			access, err := acl.AuthorizeTaskAccess(r.Context(), db, pub, userID)
			if err != nil {
				writeAPIError(w, err)
				return
			}
			rec := access.Task
			wsPubID, err := acl.ResolveWorkspacePublicByID(r.Context(), db, rec.WorkspaceID)
			if err != nil {
				// Workspace-not-found leaks as task-not-found to avoid
				// disclosing existence of cross-tenant tasks. Transport
				// errors propagate as INTERNAL.UNEXPECTED via writeAPIError.
				if hasSpec(err, apierrors.WsWorkspaceNotFound) {
					writeSpecError(w, apierrors.WsTaskNotFound)
					return
				}
				writeAPIError(w, err)
				return
			}
			prjPubID, err := acl.ResolveProjectPublicByID(r.Context(), db, rec.ProjectID, apierrors.WsTaskNotFound)
			if err != nil {
				writeAPIError(w, err)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, rec.WorkspaceID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, wsPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, access.WorkspaceRole)
			ctx = context.WithValue(ctx, ctxKeyProjectID, rec.ProjectID)
			ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, prjPubID.UUID())
			ctx = context.WithValue(ctx, ctxKeyProjectRole, access.ProjectRole)
			ctx = context.WithValue(ctx, ctxKeyTaskID, rec.ID)
			ctx = context.WithValue(ctx, ctxKeyTaskIDPublic, pub)
			ctx = enrichLoggerWithWorkspace(ctx, rec.WorkspaceID, wsPubID.UUID())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TaskVisibilityFilter returns a SQL WHERE fragment and associated bind
// arguments that enforce Layer 4 task visibility in list queries.
// See [acl.TaskVisibilityFilter] for the full contract.
func TaskVisibilityFilter(userID uint32, wsRole WorkspaceRole) (fragment string, args []any) {
	return acl.TaskVisibilityFilter(userID, wsRole)
}

// RequireProjectRole returns a middleware that asserts the actor's project
// role (previously injected by [RequireProjectMember]) meets the given
// minimum. Workspace owners and admins always pass even with an empty
// project role.
//
// Responds 403 WS.PROJECT.ACCESS_DENIED on failure.
func RequireProjectRole(minRole ProjectRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prj, ok := ProjectFromContext(r.Context())
			if !ok {
				writeSpecError(w, apierrors.WsProjectAccessDenied)
				return
			}
			if prj.Role == ProjectRoleElevated {
				next.ServeHTTP(w, r)
				return
			}
			if !prj.Role.AtLeast(minRole) {
				writeSpecError(w, apierrors.WsProjectAccessDenied)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
