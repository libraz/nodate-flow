package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"

	nflog "github.com/libraz/nodate-flow/apps/auth-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// TestLoggerContext_AttachesAttrs runs an HTTP request through the
// middleware with a pre-populated auth context and verifies that the
// downstream logger emits records carrying request_id, the session
// public id, and workspace_public_id — and no internal numeric id.
func TestLoggerContext_AttachesAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	wsPub := uuid.New()
	sessionPub := dbtype.FromUUID(uuid.New())
	// Sentinel row ids. They stay small enough to read as internal
	// sequence values, but distinctive enough that no incidental attr a
	// log line may grow later — a status code, a count, a duration —
	// equals one of them.
	const wsID = uint32(4242)
	const userID = uint32(7007)
	const requestID = "rid-test"

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := nflog.LoggerFromContext(r.Context())
		l.LogAttrs(r.Context(), slog.LevelInfo, "downstream",
			slog.String("event", "ok"),
		)
		w.WriteHeader(http.StatusNoContent)
	})

	stack := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := nflog.WithLogger(r.Context(), base)
			ctx = nflog.WithRequestID(ctx, requestID)
			ctx = authn.WithActor(ctx, userID)
			ctx = authn.WithSessionPublicID(ctx, sessionPub)
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
	if rec1["session_public_id"] != sessionPub.String() {
		t.Fatalf("session_public_id: got %v want %s", rec1["session_public_id"], sessionPub.String())
	}
	if rec1["workspace_public_id"] != wsPub.String() {
		t.Fatalf("workspace_public_id: got %v want %s", rec1["workspace_public_id"], wsPub.String())
	}
	// The internal ids that used to ride along on every request-scoped
	// line must not be there, under any key.
	for _, key := range []string{"actor_id", "workspace_id", "user_id"} {
		if _, ok := rec1[key]; ok {
			t.Fatalf("%s must not be on the request-scoped logger: %v", key, rec1)
		}
	}
	// The absence loop above covers the keys the middleware is known to
	// have written. The record must also be free of the internal ids
	// under a key nobody named, so walk every decoded value instead.
	if path := sentinelPath(rec1, "", wsID, userID); path != "" {
		t.Fatalf("an internal id reached the log line at %s: %v", path, rec1)
	}
}

// sentinelPath reports the location of the first value equal to one of
// the sentinel ids, at any depth, or "" when none is present. It reads
// the decoded record rather than the serialised line because the line
// also carries the handler's timestamp, whose digits say nothing about
// what the middleware logged.
//
// A leaked id can surface either as a JSON number, which decodes to
// float64, or as a string, so both forms count. The comparison is by
// whole value: a value is a leak when it is the id, not when it merely
// contains the id's digits.
func sentinelPath(v any, path string, sentinels ...uint32) string {
	switch t := v.(type) {
	case map[string]any:
		for key, elem := range t {
			if hit := sentinelPath(elem, path+"."+key, sentinels...); hit != "" {
				return hit
			}
		}
	case []any:
		for i, elem := range t {
			if hit := sentinelPath(elem, fmt.Sprintf("%s[%d]", path, i), sentinels...); hit != "" {
				return hit
			}
		}
	case float64:
		for _, s := range sentinels {
			if t == float64(s) {
				return path
			}
		}
	case string:
		for _, s := range sentinels {
			if t == strconv.FormatUint(uint64(s), 10) {
				return path
			}
		}
	}
	return ""
}

// TestLoggerContext_OmitsEmpty verifies that when neither actor nor
// workspace is on the context, the middleware does not add zero-valued
// attrs to the logger.
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
	if _, ok := rec1["session_public_id"]; ok {
		t.Fatalf("session_public_id should be absent: %v", rec1)
	}
	if _, ok := rec1["workspace_public_id"]; ok {
		t.Fatalf("workspace_public_id should be absent: %v", rec1)
	}
	if _, ok := rec1["request_id"]; ok {
		t.Fatalf("request_id should be absent: %v", rec1)
	}
}

// withWorkspaceForTest installs a workspace context using the same keys
// that RequireWorkspaceMember writes to.
func withWorkspaceForTest(ctx context.Context, wsID uint32, pub uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyWorkspaceID, wsID)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleMember)
	return ctx
}
