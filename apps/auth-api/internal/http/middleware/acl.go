// Package middleware contains chi-compatible HTTP middlewares for the
// auth-api service, including authentication and ACL (access control
// list) enforcement.
package middleware

import (
	"context"
	"database/sql"
	stderrors "errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	sharedacl "github.com/libraz/nodate-flow/packages/go-shared/acl"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// ACLDB is the minimal database surface for ACL queries that are not
// yet routed through the sqlc-generated [generated.Queries] helper.
// Workspace-membership lookups still use it; instance-admin lookups
// have been migrated to the typed query (see [RequireInstanceAdmin]).
type ACLDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// codeSpec maps a canonical error code string to its [apierrors.Spec]
// pointer. The shared instance-admin middleware ([sharedacl.RequireInstanceAdmin])
// emits errors as `(status, code, message)` tuples; this map lets the
// adapter recover the rich catalog metadata (Description / UserAction)
// before delegating to [handlerutil.WriteSpecError]. Unknown codes
// fall through to a synthetic Spec built from the supplied tuple so
// the wire shape stays RFC 9457-compliant for any future code that
// might be added to the shared package.
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
		handlerutil.WriteSpecError(w, spec)
		return
	}
	handlerutil.WriteSpecError(w, &apierrors.Spec{
		Code:    code,
		Status:  status,
		Message: message,
	})
}

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
func (r WorkspaceRole) AtLeast(minRole WorkspaceRole) bool {
	return workspaceRoleRank[r] >= workspaceRoleRank[minRole]
}

// ----------------------------------------------------------------------------
// Context plumbing
// ----------------------------------------------------------------------------

// ctxKey is an unexported type used as a context key for ACL-specific values.
type ctxKey int

const (
	ctxKeyWorkspaceID ctxKey = iota
	ctxKeyWorkspaceIDPublic
	ctxKeyWorkspaceRole
)

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

// ----------------------------------------------------------------------------
// Instance-level
// ----------------------------------------------------------------------------

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
// All responses are emitted via [handlerutil.WriteSpecError] so the
// body is RFC 9457 problem+json (type / title / status / detail).
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

// RequireWorkspaceMember returns a middleware that resolves the workspace from
// the {wsId} path parameter, verifies the actor is an enabled member, and
// injects [WorkspaceContext] into the request context. Responds:
//   - 404 WS.WORKSPACE.NOT_FOUND when the workspace does not exist or the
//     path param is not a valid UUID.
//   - 403 WS.WORKSPACE.ACCESS_DENIED when the actor is not a member.
func RequireWorkspaceMember(db ACLDB) func(http.Handler) http.Handler {
	const wsQuery = `SELECT id FROM workspaces
WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	const memQuery = `SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := authn.ActorFromContext(r.Context())
			if !ok {
				handlerutil.WriteSpecError(w, apierrors.WsWorkspaceAccessDenied)
				return
			}
			raw := chi.URLParam(r, "wsId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				handlerutil.WriteSpecError(w, apierrors.WsWorkspaceNotFound)
				return
			}
			var wsID uint32
			if err := db.QueryRowContext(r.Context(), wsQuery, types.FromUUID(pub)).Scan(&wsID); err != nil {
				if stderrors.Is(err, sql.ErrNoRows) {
					handlerutil.WriteSpecError(w, apierrors.WsWorkspaceNotFound)
					return
				}
				handlerutil.WriteSpecError(w, apierrors.InternalUnexpected)
				return
			}
			var role string
			if err := db.QueryRowContext(r.Context(), memQuery, wsID, userID).Scan(&role); err != nil {
				if stderrors.Is(err, sql.ErrNoRows) {
					handlerutil.WriteSpecError(w, apierrors.WsWorkspaceAccessDenied)
					return
				}
				handlerutil.WriteSpecError(w, apierrors.InternalUnexpected)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID, wsID)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
			ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRole(role))
			ctx = enrichLoggerWithWorkspace(ctx, wsID, pub)
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
				handlerutil.WriteSpecError(w, apierrors.WsMemberRoleDenied)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
