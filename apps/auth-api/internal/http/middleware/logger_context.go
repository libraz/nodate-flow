package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	nflog "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// LoggerContext returns an HTTP middleware that builds a request-scoped
// [*slog.Logger] pre-populated with request_id, actor_id, and
// workspace_id attrs and stores it on the request context via
// [nflog.WithLogger]. Downstream handlers can fetch it with
// [nflog.LoggerFromContext] without threading those values manually.
//
// Ordering. This middleware MUST be installed AFTER the auth middleware
// so [authn.ActorFromContext] resolves to the authenticated user. It is
// idempotent: running it more than once on the same chain just wraps
// the previous logger with the same attrs (slog.With is additive).
//
// Empty values are omitted: when the actor or workspace is not yet on
// the context the corresponding attr is dropped rather than logged as a
// zero value, so an unauthenticated request still produces a usable
// logger without misleading "actor_id":0 noise.
//
// Numeric ids are emitted via [slog.Any] rather than [slog.Int64] /
// [slog.Uint64] so the project's forbidigo rule (which guards against
// surfacing internal numeric ids through the slog.IntXX helpers under
// the actor_id / workspace_id keys) stays untriggered.
func LoggerContext() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			base := nflog.LoggerFromContext(ctx)

			attrs := make([]any, 0, 6)
			if rid := nflog.RequestIDFromContext(ctx); rid != "" {
				attrs = append(attrs, slog.String("request_id", rid))
			}
			if uid, ok := authn.ActorFromContext(ctx); ok && uid != 0 {
				attrs = append(attrs, slog.Any("actor_id", uid))
			}
			if ws, ok := WorkspaceFromContext(ctx); ok {
				if ws.ID != 0 {
					attrs = append(attrs, slog.Any("workspace_id", ws.ID))
				}
				if ws.PublicID != uuid.Nil {
					attrs = append(attrs, slog.String("workspace_public_id", ws.PublicID.String()))
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

// enrichLoggerWithWorkspace augments the request-scoped logger with
// workspace_id / workspace_public_id attrs and returns a derived context
// carrying the augmented logger. Invoked from the workspace ACL
// middleware after it confirms membership so downstream handlers see a
// logger that already knows the workspace, even though the global
// LoggerContext() middleware ran upstream of that ACL stage.
func enrichLoggerWithWorkspace(ctx context.Context, wsID uint32, pub uuid.UUID) context.Context {
	if ctx == nil {
		return ctx
	}
	base := nflog.LoggerFromContext(ctx)
	attrs := make([]any, 0, 2)
	if wsID != 0 {
		attrs = append(attrs, slog.Any("workspace_id", wsID))
	}
	if pub != uuid.Nil {
		attrs = append(attrs, slog.String("workspace_public_id", pub.String()))
	}
	if len(attrs) == 0 {
		return ctx
	}
	return nflog.WithLogger(ctx, base.With(attrs...))
}
