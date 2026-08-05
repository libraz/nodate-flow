package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
)

// TestLoggerContext_AttachesAttrs runs an HTTP request through the
// middleware with a pre-populated auth context and verifies that the
// downstream logger emits records carrying request_id, actor_id, and
// workspace_id / workspace_public_id attrs.
func TestLoggerContext_AttachesAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	wsPub := uuid.New()
	const wsID = uint32(42)
	const userID = uint32(7)
	const requestID = "rid-test"

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := nflog.LoggerFromContext(r.Context())
		l.LogAttrs(r.Context(), slog.LevelInfo, "downstream",
			slog.String("event", "ok"),
		)
		w.WriteHeader(http.StatusNoContent)
	})

	// Stub upstream: install a request-scoped base logger + request_id +
	// actor + workspace context, mimicking what RequestLogger / RequireAuth
	// / RequireWorkspaceMember do in production. Then run LoggerContext()
	// and the final handler.
	stack := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := nflog.WithLogger(r.Context(), base)
			ctx = nflog.WithRequestID(ctx, requestID)
			ctx = WithActor(ctx, userID)
			ctx = withWorkspaceForTest(ctx, wsID, wsPub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	stack(LoggerContext()(final)).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", rec.Code)
	}

	// Decode the captured JSON record so we don't depend on the exact
	// field order chosen by the JSON handler.
	var rec1 map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec1); err != nil {
		t.Fatalf("decode log line: %v\nraw: %s", err, buf.String())
	}
	if rec1["msg"] != "downstream" {
		t.Fatalf("msg: got %v want downstream", rec1["msg"])
	}
	if rec1["request_id"] != requestID {
		t.Fatalf("request_id: got %v want %s", rec1["request_id"], requestID)
	}
	if rec1["actor_id"] == nil {
		t.Fatal("actor_id missing from record")
	}
	if rec1["workspace_id"] == nil {
		t.Fatal("workspace_id missing from record")
	}
	if rec1["workspace_public_id"] != wsPub.String() {
		t.Fatalf("workspace_public_id: got %v want %s", rec1["workspace_public_id"], wsPub.String())
	}
}

// TestLoggerContext_OmitsEmpty verifies that when neither actor nor
// workspace is on the context (e.g. an unauthenticated request reaches
// the middleware due to a routing mistake), the middleware does not add
// zero-valued attrs to the logger.
func TestLoggerContext_OmitsEmpty(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nflog.LoggerFromContext(r.Context()).LogAttrs(r.Context(), slog.LevelInfo, "anon")
		w.WriteHeader(http.StatusOK)
	})

	stack := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := nflog.WithLogger(r.Context(), base)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	stack(LoggerContext()(final)).ServeHTTP(rec, req)

	var rec1 map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec1); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	if _, ok := rec1["actor_id"]; ok {
		t.Fatalf("actor_id should be absent: %v", rec1)
	}
	if _, ok := rec1["workspace_id"]; ok {
		t.Fatalf("workspace_id should be absent: %v", rec1)
	}
	if _, ok := rec1["request_id"]; ok {
		t.Fatalf("request_id should be absent: %v", rec1)
	}
}

// withWorkspaceForTest installs a workspace context using the same keys
// that RequireWorkspaceMember writes to. It lives here (in the package's
// test file) because the production injection helpers are intentionally
// unexported.
func withWorkspaceForTest(ctx context.Context, wsID uint32, pub uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyWorkspaceID, wsID)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleMember)
	return ctx
}
