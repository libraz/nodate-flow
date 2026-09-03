package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"

	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
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
	// Renaming the key is not a fix, so the values are checked too — but
	// against the decoded attributes rather than the raw line. Searching
	// the line for ":42" also searches the handler's own timestamp, where
	// that text appears whenever the minute or second happens to be 42.
	for key, val := range rec1 {
		if key == slog.TimeKey {
			continue
		}
		for _, id := range []string{strconv.FormatUint(uint64(wsID), 10), strconv.FormatUint(uint64(userID), 10)} {
			if attrCarriesValue(val, id) {
				t.Fatalf("internal id %s reached the request-scoped logger under %q: %v", id, key, rec1)
			}
		}
	}
}

// attrCarriesValue reports whether a decoded log attribute holds the
// given value, at any depth.
//
// It compares whole values rather than searching for the text, because
// the record also carries public ids: a UUID has a one-in-a-few chance
// of containing any short digit string, and a check that reacted to
// that would fail on the run that generated the wrong UUID.
func attrCarriesValue(v any, want string) bool {
	switch val := v.(type) {
	case map[string]any:
		for _, nested := range val {
			if attrCarriesValue(nested, want) {
				return true
			}
		}
	case []any:
		for _, nested := range val {
			if attrCarriesValue(nested, want) {
				return true
			}
		}
	case float64:
		// JSON has one number type, so an integer attr arrives as a float.
		return strconv.FormatFloat(val, 'f', -1, 64) == want
	case string:
		return val == want
	}
	return false
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
// that RequireWorkspaceMember writes to. It lives here (in the package's
// test file) because the production injection helpers are intentionally
// unexported.
func withWorkspaceForTest(ctx context.Context, wsID uint32, pub uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyWorkspaceID, wsID)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleMember)
	return ctx
}
