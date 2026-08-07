package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/httputil"
	"github.com/libraz/nodate-flow/packages/go-shared/problem"
)

// TestEveryLayerAnswersInOneShape drives a real rejection out of each
// layer that can end a request early, and compares the bodies.
//
// The layers are independent of one another — a shared authentication
// middleware, a shared rate limiter, this package's ACL and service
// token guards, a raw handler writer — and each used to answer in
// whatever shape it had grown. A client cannot pick which one rejects
// it, so any layer answering differently is a client that cannot read
// the answer: the SDK builds its error from `type` / `detail` /
// `status`, and an envelope without them yields an error with no code
// to branch on and no status. The frontend then treats an expired
// session as a connection blip and leaves the user apparently signed
// in, and the CLI has nothing to print but its own fallback.
//
// The comparison is between the layers rather than against a copy of
// the expected shape kept here, so the test cannot pass by having the
// same mistake made in two places.
func TestEveryLayerAnswersInOneShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		layer  string
		status int
		code   string
		serve  func() *httptest.ResponseRecorder
	}{
		{
			layer:  "authentication (401)",
			status: http.StatusUnauthorized,
			code:   "AUTH.TOKEN.MISSING_OR_MALFORMED",
			serve: func() *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				authn.RequireAuth()(noContent()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				return rec
			},
		},
		{
			layer:  "service token (401)",
			status: http.StatusUnauthorized,
			code:   "AUTH.TOKEN.MISSING_OR_MALFORMED",
			serve: func() *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				middleware.RequireServiceTokenOnly("")(noContent()).
					ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/x", nil))
				return rec
			},
		},
		{
			layer:  "acl (403)",
			status: http.StatusForbidden,
			code:   "AUTH.PAT.SCOPE_INSUFFICIENT",
			serve: func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPatch, "/test", nil)
				ctx := authn.WithTokenKind(req.Context(), authn.TokenKindPAT)
				ctx = authn.WithTokenScopes(ctx, []string{"read:workspace"})
				rec := httptest.NewRecorder()
				middleware.RequireBearerTokenScope(noContent()).ServeHTTP(rec, req.WithContext(ctx))
				return rec
			},
		},
		{
			layer:  "rate limit (429)",
			status: http.StatusTooManyRequests,
			code:   "RATE.LIMIT.EXCEEDED",
			serve: func() *httptest.ResponseRecorder {
				rl := httputil.NewIPRateLimiter(httputil.RateLimitConfig{MaxRequests: 1, Window: time.Minute})
				defer rl.Stop()
				handler := rl.Middleware()(noContent())
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req = req.WithContext(middleware.WithClientIP(req.Context(), "10.0.0.9"))
				handler.ServeHTTP(httptest.NewRecorder(), req)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			},
		},
		{
			layer:  "handler (4xx)",
			status: apierrors.WsWorkspaceNotFound.Status,
			code:   apierrors.WsWorkspaceNotFound.Code,
			serve: func() *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				handlerutil.WriteSpecError(rec, apierrors.WsWorkspaceNotFound)
				return rec
			},
		},
	}

	var members []string
	for _, tc := range cases {
		rec := tc.serve()

		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.layer, rec.Code, tc.status)
		}
		if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
			t.Errorf("%s: Content-Type = %q, want %q", tc.layer, ct, problem.ContentType)
		}

		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("%s: body is not JSON: %v", tc.layer, err)
		}
		if body["type"] != tc.code {
			t.Errorf("%s: type = %v, want %q", tc.layer, body["type"], tc.code)
		}
		if body["status"] != float64(tc.status) {
			t.Errorf("%s: status member = %v, want %d", tc.layer, body["status"], tc.status)
		}
		if body["title"] != http.StatusText(tc.status) {
			t.Errorf("%s: title = %v, want %q", tc.layer, body["title"], http.StatusText(tc.status))
		}
		if detail, ok := body["detail"].(string); !ok || detail == "" {
			t.Errorf("%s: detail = %v, want a message for the user", tc.layer, body["detail"])
		}
		for _, gone := range []string{"code", "message"} {
			if _, present := body[gone]; present {
				t.Errorf("%s: %q must be gone from the envelope, not merely unread: %v", tc.layer, gone, body)
			}
		}

		// The members every layer carries. Optional catalog fields
		// (description, userAction, extensions) are dropped from the
		// comparison: they depend on what the error catalog says about
		// the code, not on which layer wrote it.
		var required []string
		for k := range body {
			switch k {
			case "description", "userAction", "extensions":
			default:
				required = append(required, k)
			}
		}
		sort.Strings(required)
		got := strings.Join(required, ",")
		if members == nil {
			members = required
			continue
		}
		if want := strings.Join(members, ","); got != want {
			t.Errorf("%s: envelope members are %s, but an earlier layer answered with %s", tc.layer, got, want)
		}
	}

	if want := "detail,status,title,type"; strings.Join(members, ",") != want {
		t.Errorf("the shared envelope carries %s, want %s — the SDK reads all four",
			strings.Join(members, ","), want)
	}
}

func noContent() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}
