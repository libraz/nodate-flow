package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	sharedacl "github.com/libraz/nodate-flow/packages/go-shared/acl"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// problem is the decoded RFC 9457 problem+json envelope used by the
// tests below to assert the wire shape.
type problem struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Status      int    `json:"status"`
	Detail      string `json:"detail"`
	Description string `json:"description,omitempty"`
	UserAction  string `json:"userAction,omitempty"`
}

// TestWriteSpecErrorByCode_Unauthorized verifies that the WriteError
// adapter passed to [sharedacl.RequireInstanceAdmin] maps the 401
// AUTH.SESSION.UNAUTHORIZED code to the canonical Spec and emits an
// RFC 9457 problem+json envelope with the catalog message.
func TestWriteSpecErrorByCode_Unauthorized(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)

	writeSpecErrorByCode(rec, req,
		http.StatusUnauthorized,
		sharedacl.CodeSessionUnauthorized,
		"any default message",
	)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, apierrors.AuthSessionUnauthorized.Code, p.Type,
		"type should be the canonical error code")
	assert.Equal(t, http.StatusText(http.StatusUnauthorized), p.Title,
		"title should be the HTTP status text")
	assert.Equal(t, http.StatusUnauthorized, p.Status)
	assert.Equal(t, apierrors.AuthSessionUnauthorized.Message, p.Detail,
		"detail should come from the canonical Spec, not the supplied tuple message")
}

// TestWriteSpecErrorByCode_Forbidden verifies the 403 path maps to the
// canonical AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED Spec.
func TestWriteSpecErrorByCode_Forbidden(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)

	writeSpecErrorByCode(rec, req,
		http.StatusForbidden,
		sharedacl.CodeInstanceAdminRequired,
		"any default message",
	)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, apierrors.AuthPermissionInstanceAdminRequired.Code, p.Type)
	assert.Equal(t, http.StatusText(http.StatusForbidden), p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, apierrors.AuthPermissionInstanceAdminRequired.Message, p.Detail)
}

// TestWriteSpecErrorByCode_UnknownCodeFallsThrough verifies that a
// code not in [codeSpec] still produces an RFC 9457 envelope built
// from the supplied tuple, so future shared-package codes do not
// crash the middleware.
func TestWriteSpecErrorByCode_UnknownCodeFallsThrough(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)

	writeSpecErrorByCode(rec, req, http.StatusTeapot, "FUTURE.UNKNOWN.CODE", "future message")

	require.Equal(t, http.StatusTeapot, rec.Code)
	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "FUTURE.UNKNOWN.CODE", p.Type)
	assert.Equal(t, http.StatusText(http.StatusTeapot), p.Title)
	assert.Equal(t, http.StatusTeapot, p.Status)
	assert.Equal(t, "future message", p.Detail)
}

// TestRequireInstanceAdmin_EmitsRFC9457OnDeny is an integration test
// that exercises the shared-package middleware with the same callback
// configuration [RequireInstanceAdmin] uses, and asserts the response
// body is RFC 9457 problem+json.
//
// The IsInstanceAdmin callback returns false so the middleware
// short-circuits with 403 AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED;
// the actor extractor returns a known user id so the middleware does
// not return 401 first.
func TestRequireInstanceAdmin_EmitsRFC9457OnDeny(t *testing.T) {
	t.Parallel()

	mw := sharedacl.RequireInstanceAdmin(sharedacl.Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) {
			return false, nil
		},
		ExtractActor: func(r *http.Request) (uint32, bool) {
			return authn.ActorFromContext(r.Context())
		},
		WriteError: writeSpecErrorByCode,
	})

	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler must not run when actor lacks the role")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	req = req.WithContext(authn.WithActor(req.Context(), 7))
	mw(final).ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED", p.Type)
	assert.Equal(t, "Forbidden", p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.NotEmpty(t, p.Detail, "detail must carry the human-readable message")
}

// TestRequireInstanceAdmin_EmitsRFC9457OnMissingActor is the 401
// counterpart: the actor extractor reports no actor so the
// middleware short-circuits with AUTH.SESSION.UNAUTHORIZED. The body
// must still be RFC 9457 shaped.
func TestRequireInstanceAdmin_EmitsRFC9457OnMissingActor(t *testing.T) {
	t.Parallel()

	mw := sharedacl.RequireInstanceAdmin(sharedacl.Config{
		IsInstanceAdmin: func(context.Context, uint32) (bool, error) {
			t.Fatal("IsInstanceAdmin must not be called when actor is missing")
			return false, nil
		},
		ExtractActor: func(*http.Request) (uint32, bool) {
			return 0, false
		},
		WriteError: writeSpecErrorByCode,
	})

	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler must not run when actor is missing")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
	mw(final).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "AUTH.SESSION.UNAUTHORIZED", p.Type)
	assert.Equal(t, "Unauthorized", p.Title)
	assert.Equal(t, http.StatusUnauthorized, p.Status)
}

// TestRequireWorkspaceRole_EmitsRFC9457OnDeny pins the deny body.
// [RequireWorkspaceRole] must refuse through
// [handlerutil.WriteSpecError], so the response conforms to RFC 9457
// rather than the bare `{"code":"...","message":"..."}` shape an
// ad-hoc error writer produces.
func TestRequireWorkspaceRole_EmitsRFC9457OnDeny(t *testing.T) {
	t.Parallel()

	mw := RequireWorkspaceRole(WorkspaceRoleAdmin)

	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler must not run when role is below minimum")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/x", nil)
	// Inject a member-level workspace context so the role check fires
	// rather than the missing-context branch.
	ctx := context.WithValue(req.Context(), ctxKeyWorkspaceID, uint32(1))
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleMember)
	mw(final).ServeHTTP(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "WS.MEMBER.ROLE_DENIED", p.Type, "type must be the canonical error code, not a bare 'code' field")
	assert.Equal(t, "Forbidden", p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.NotEmpty(t, p.Detail)
}
