package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ACLDB is the minimal database surface for ACL queries.
type ACLDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
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

// RequireInstanceAdmin checks the instance_admins table for the authenticated
// user and rejects the request with 403 if no active grant exists.
func RequireInstanceAdmin(db ACLDB) func(http.Handler) http.Handler {
	const q = `SELECT 1 FROM instance_admins WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL LIMIT 1`
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := authn.ActorFromContext(r.Context())
			if !ok {
				writeError(w, apierrors.InstanceAdminRequired.Status,
					apierrors.InstanceAdminRequired.Code,
					apierrors.InstanceAdminRequired.Message)
				return
			}
			var one int
			err := db.QueryRowContext(r.Context(), q, userID).Scan(&one)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, apierrors.InstanceAdminRequired.Status,
						apierrors.InstanceAdminRequired.Code,
						apierrors.InstanceAdminRequired.Message)
					return
				}
				writeError(w, 500, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
				writeError(w, http.StatusForbidden,
					apierrors.WsWorkspaceAccessDenied.Code,
					apierrors.WsWorkspaceAccessDenied.Message)
				return
			}
			raw := chi.URLParam(r, "wsId")
			pub, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusNotFound,
					apierrors.WsWorkspaceNotFound.Code,
					apierrors.WsWorkspaceNotFound.Message)
				return
			}
			var wsID uint32
			if err := db.QueryRowContext(r.Context(), wsQuery, types.FromUUID(pub)).Scan(&wsID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound,
						apierrors.WsWorkspaceNotFound.Code,
						apierrors.WsWorkspaceNotFound.Message)
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			var role string
			if err := db.QueryRowContext(r.Context(), memQuery, wsID, userID).Scan(&role); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusForbidden,
						apierrors.WsWorkspaceAccessDenied.Code,
						apierrors.WsWorkspaceAccessDenied.Message)
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
func RequireWorkspaceRole(minRole WorkspaceRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, ok := WorkspaceFromContext(r.Context())
			if !ok || !ws.Role.AtLeast(minRole) {
				writeError(w, http.StatusForbidden,
					apierrors.WsMemberRoleDenied.Code,
					apierrors.WsMemberRoleDenied.Message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
