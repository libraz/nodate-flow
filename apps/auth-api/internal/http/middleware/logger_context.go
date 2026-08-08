package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	nflog "github.com/libraz/nodate-flow/apps/auth-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// LoggerContext returns an HTTP middleware that builds a request-scoped
// [*slog.Logger] pre-populated with request_id, session and workspace
// attrs and stores it on the request context via [nflog.WithLogger].
// Downstream handlers can fetch it with [nflog.LoggerFromContext]
// without threading those values manually.
//
// Ordering. This middleware MUST be installed AFTER the auth middleware
// so [authn.SessionPublicIDFromContext] resolves to the caller's
// session. It is idempotent: running it more than once on the same chain
// just wraps the previous logger with the same attrs (slog.With is
// additive).
//
// Empty values are omitted: when the session or workspace is not yet on
// the context the corresponding attr is dropped rather than logged as a
// zero value, so an unauthenticated request still produces a usable
// logger without misleading zero-valued noise.
//
// The caller is identified by session public id rather than by the
// internal user id. Attaching the numeric id here put it on every log
// line the request produced, which is the widest reach an internal
// sequence can get; the session public id correlates the same lines
// without carrying one.
func LoggerContext() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			base := nflog.LoggerFromContext(ctx)

			attrs := make([]any, 0, 6)
			if rid := nflog.RequestIDFromContext(ctx); rid != "" {
				attrs = append(attrs, slog.String("request_id", rid))
			}
			if sid, ok := authn.SessionPublicIDFromContext(ctx); ok {
				attrs = append(attrs, logutil.LogEntityPID("session", sid))
			}
			if ws, ok := WorkspaceFromContext(ctx); ok {
				if ws.PublicID != uuid.Nil {
					attrs = append(attrs, logutil.LogEntity("workspace", ws.PublicID))
				}
			}

			if len(attrs) > 0 {
				scoped := base.With(attrs...)
				ctx = nflog.WithLogger(ctx, scoped)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// enrichLoggerWithWorkspace augments the request-scoped logger with the
// workspace_public_id attr and returns a derived context carrying the
// augmented logger. Invoked from the workspace ACL middleware after it
// confirms membership so downstream handlers see a logger that already
// knows the workspace, even though the global LoggerContext() middleware
// ran upstream of that ACL stage.
func enrichLoggerWithWorkspace(ctx context.Context, pub uuid.UUID) context.Context {
	if ctx == nil || pub == uuid.Nil {
		return ctx
	}
	base := nflog.LoggerFromContext(ctx)
	return nflog.WithLogger(ctx, base.With(logutil.LogEntity("workspace", pub)))
}
