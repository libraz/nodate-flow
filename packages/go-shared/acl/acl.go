// Package acl provides shared HTTP middleware that enforces
// instance-level access control for both flow-api and auth-api.
//
// The middleware delegates database lookups, actor extraction, and
// error rendering to caller-supplied callbacks so that this package
// can serve multiple apps without taking a dependency on any
// app-specific generated query types or error catalog wiring.
//
// Apps typically wire it as:
//
//	mw := acl.RequireInstanceAdmin(acl.Config{
//	    IsInstanceAdmin: func(ctx context.Context, uid uint32) (bool, error) {
//	        _, err := q.FindInstanceAdminByUserId(ctx, uid)
//	        if errors.Is(err, sql.ErrNoRows) {
//	            return false, nil
//	        }
//	        return err == nil, err
//	    },
//	    ExtractActor: authn.ActorFromContext,
//	    WriteError:   handlerutil.HTTPErr,
//	})
package acl

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// Error code constants emitted by [RequireInstanceAdmin] via
// [Config.WriteError]. These names mirror the canonical error codes in
// errors/*.yaml so that downstream catalog wiring can map them to the
// correct ProblemDetails payload, status text, and i18n strings.
const (
	// CodeSessionUnauthorized is reported with HTTP 401 when no
	// authenticated actor is present on the request context.
	CodeSessionUnauthorized = "AUTH.SESSION.UNAUTHORIZED"

	// CodeInstanceAdminRequired is reported with HTTP 403 when the
	// authenticated actor does not have the instance-admin role.
	CodeInstanceAdminRequired = "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED"

	// CodeInternalUnexpected is reported with HTTP 500 when the
	// IsInstanceAdmin callback returns an unexpected error (transport
	// failure, query timeout, etc.).
	CodeInternalUnexpected = "INTERNAL.UNEXPECTED"
)

// Default user-facing messages emitted alongside the error codes
// above. Apps that want catalog-driven copy should ignore these and
// translate the code in their own [ErrorWriter].
const (
	msgSessionUnauthorized   = "Authentication is required"
	msgInstanceAdminRequired = "Instance administrator privileges are required"
	msgInternalUnexpected    = "Internal error"
)

// IsInstanceAdminFunc reports whether the given user has the
// instance-admin role. Implementations typically call something like
// `Queries.FindInstanceAdminByUserId(ctx, userID)` and translate
// [database/sql.ErrNoRows] to (false, nil). Any non-nil error is
// surfaced as HTTP 500 [CodeInternalUnexpected] by the middleware.
type IsInstanceAdminFunc func(ctx context.Context, userID uint32) (bool, error)

// ActorIDExtractor returns the authenticated user's internal numeric
// id from the request context. Apps wire their existing session
// middleware's context-key accessor here (e.g.
// `authn.ActorFromContext`). The boolean must be false when no actor
// is present.
type ActorIDExtractor func(r *http.Request) (uint32, bool)

// ErrorWriter renders a structured error response. Apps inject their
// RFC 9457 ProblemDetails writer (e.g. `handlerutil.HTTPErr`) so the
// middleware can stay agnostic of wire format and error catalog
// conventions.
type ErrorWriter func(w http.ResponseWriter, r *http.Request, status int, code, message string)

// Config wires the three required callbacks. All fields are
// mandatory; [RequireInstanceAdmin] panics at construction time when
// any of them is nil to fail loudly during boot rather than silently
// misbehaving at request time.
type Config struct {
	// IsInstanceAdmin performs the role lookup. Required.
	IsInstanceAdmin IsInstanceAdminFunc
	// ExtractActor reads the authenticated user's id from the request
	// context. Required.
	ExtractActor ActorIDExtractor
	// WriteError renders the structured error response. Required.
	WriteError ErrorWriter
}

// RequireInstanceAdmin returns a chi-compatible HTTP middleware that
// allows the request through only when the authenticated actor has
// the instance-admin role.
//
// The middleware:
//
//  1. Calls [Config.ExtractActor]; if the actor is missing it writes
//     401 [CodeSessionUnauthorized] via [Config.WriteError].
//  2. Calls [Config.IsInstanceAdmin]; if the callback returns an
//     error it writes 500 [CodeInternalUnexpected]; if it returns
//     false it writes 403 [CodeInstanceAdminRequired].
//  3. Calls next.ServeHTTP unchanged on success.
//
// The function panics when any callback in cfg is nil. This is
// intentional: a misconfigured ACL middleware is a deployment bug
// that must crash the process at boot, not silently deny or allow
// requests.
func RequireInstanceAdmin(cfg Config) func(http.Handler) http.Handler {
	if cfg.IsInstanceAdmin == nil {
		panic("acl.RequireInstanceAdmin: Config.IsInstanceAdmin is nil")
	}
	if cfg.ExtractActor == nil {
		panic("acl.RequireInstanceAdmin: Config.ExtractActor is nil")
	}
	if cfg.WriteError == nil {
		panic("acl.RequireInstanceAdmin: Config.WriteError is nil")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, ok := cfg.ExtractActor(r)
			if !ok {
				slog.Default().WarnContext(r.Context(),
					"instance admin check rejected: no actor on request context",
					slog.Group("acl",
						slog.String("check", "instance_admin"),
						slog.String("reason", "actor_missing"),
					),
				)
				cfg.WriteError(w, r, http.StatusUnauthorized,
					CodeSessionUnauthorized, msgSessionUnauthorized)
				return
			}

			isAdmin, err := cfg.IsInstanceAdmin(r.Context(), uid)
			if err != nil {
				slog.Default().ErrorContext(r.Context(),
					"instance admin lookup failed",
					slog.Group("acl",
						slog.String("check", "instance_admin"),
						logutil.LogNumber("user_id", uid),
						slog.String("error", err.Error()),
					),
				)
				cfg.WriteError(w, r, http.StatusInternalServerError,
					CodeInternalUnexpected, msgInternalUnexpected)
				return
			}
			if !isAdmin {
				slog.Default().InfoContext(r.Context(),
					"instance admin check denied",
					slog.Group("acl",
						slog.String("check", "instance_admin"),
						logutil.LogNumber("user_id", uid),
						slog.String("reason", "not_instance_admin"),
					),
				)
				cfg.WriteError(w, r, http.StatusForbidden,
					CodeInstanceAdminRequired, msgInstanceAdminRequired)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
