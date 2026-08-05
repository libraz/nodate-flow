package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	sharedacl "github.com/libraz/nodate-flow/packages/go-shared/acl"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// The canonical role / visibility tests live in
// apps/flow-api/internal/acl/acl_test.go. The wrapper tests below only
// verify that the chi-side context plumbing exposes the same shape via
// the re-exported aliases.

func TestWorkspaceRoleAlias(t *testing.T) {
	t.Parallel()
	if !WorkspaceRoleOwner.AtLeast(WorkspaceRoleAdmin) {
		t.Fatal("alias WorkspaceRole.AtLeast not wired to acl package")
	}
	if WorkspaceRoleMember.AtLeast(WorkspaceRoleAdmin) {
		t.Fatal("alias WorkspaceRole.AtLeast inverted")
	}
}

func TestProjectRoleAlias(t *testing.T) {
	t.Parallel()
	if !ProjectRoleLead.AtLeast(ProjectRoleEditor) {
		t.Fatal("alias ProjectRole.AtLeast not wired to acl package")
	}
}

func TestActorFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := ActorFromContext(context.Background()); ok {
		t.Fatal("expected no actor in empty context")
	}
	ctx := WithActor(context.Background(), 42)
	id, ok := ActorFromContext(ctx)
	if !ok || id != 42 {
		t.Fatalf("got id=%d ok=%v want 42 true", id, ok)
	}
}

func TestWorkspaceFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := WorkspaceFromContext(context.Background()); ok {
		t.Fatal("expected no workspace in empty context")
	}
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyWorkspaceID, uint32(7))
	ctx = context.WithValue(ctx, ctxKeyWorkspaceIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleAdmin)
	ws, ok := WorkspaceFromContext(ctx)
	if !ok || ws.ID != 7 || ws.PublicID != pub || ws.Role != WorkspaceRoleAdmin {
		t.Fatalf("unexpected ws=%+v ok=%v", ws, ok)
	}
}

func TestProjectFromContext(t *testing.T) {
	t.Parallel()
	if _, ok := ProjectFromContext(context.Background()); ok {
		t.Fatal("expected no project in empty context")
	}
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRoleEditor)
	prj, ok := ProjectFromContext(ctx)
	if !ok || prj.ID != 11 || prj.PublicID != pub || prj.Role != ProjectRoleEditor {
		t.Fatalf("unexpected prj=%+v ok=%v", prj, ok)
	}
}

// TestProjectFromContext_InvalidRoleReportsAbsent verifies that a
// corrupted role value (e.g. an unknown enum string written by a
// stale schema migration or a manual DB edit) is treated as a
// server-side invariant violation — the context lookup returns
// ok=false so callers surface 500 INTERNAL.UNEXPECTED rather than
// silently falling through to a permissive default role or
// producing a misleading 403.
func TestProjectFromContext_InvalidRoleReportsAbsent(t *testing.T) {
	t.Parallel()
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRole("not_a_real_role"))
	if _, ok := ProjectFromContext(ctx); ok {
		t.Fatal("ProjectFromContext returned ok=true for an unknown role string")
	}
}

// TestProjectFromContext_ElevatedIsValid verifies that the elevated
// marker (empty string) is accepted as valid — workspace owners /
// admins reach a project without a per-project role and the
// middleware records that with [ProjectRoleElevated].
func TestProjectFromContext_ElevatedIsValid(t *testing.T) {
	t.Parallel()
	pub := uuid.New()
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, pub)
	ctx = context.WithValue(ctx, ctxKeyProjectRole, ProjectRoleElevated)
	prj, ok := ProjectFromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true for ProjectRoleElevated marker")
	}
	if prj.Role != ProjectRoleElevated {
		t.Fatalf("expected role=elevated, got %q", prj.Role)
	}
}

func TestTaskVisibilityFilterDelegation(t *testing.T) {
	t.Parallel()
	// Admin sees everything -> empty fragment.
	frag, args := TaskVisibilityFilter(1, WorkspaceRoleAdmin)
	if frag != "" || len(args) != 0 {
		t.Fatalf("admin: got frag=%q args=%v", frag, args)
	}
	// Member -> non-empty fragment with three bound args.
	frag, args = TaskVisibilityFilter(42, WorkspaceRoleMember)
	if frag == "" || len(args) != 3 {
		t.Fatalf("member: got frag=%q args=%v", frag, args)
	}
}

// problemBodyDecoded mirrors [problemBody] for tests that decode the
// wire payload. Re-declared in the test file so the assertions are
// independent of the production struct (e.g. an accidental rename of
// the JSON tag would still flag a test failure).
type problemBodyDecoded struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Status      int    `json:"status"`
	Detail      string `json:"detail"`
	Description string `json:"description,omitempty"`
	UserAction  string `json:"userAction,omitempty"`
}

// TestWriteSpecError_RFC9457Shape verifies the chi-level error writer
// emits the canonical RFC 9457 problem+json envelope.
func TestWriteSpecError_RFC9457Shape(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeSpecError(rec, apierrors.WsTaskAccessDenied)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "WS.TASK.ACCESS_DENIED", p.Type)
	assert.Equal(t, "Forbidden", p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.NotEmpty(t, p.Detail, "detail must carry the human-readable message")
}

// TestWriteSpecErrorByCode_Unauthorized verifies the WriteError adapter
// passed to [sharedacl.RequireInstanceAdmin] maps the 401
// AUTH.SESSION.UNAUTHORIZED code to the canonical Spec.
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

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, apierrors.AuthSessionUnauthorized.Code, p.Type)
	assert.Equal(t, http.StatusText(http.StatusUnauthorized), p.Title)
	assert.Equal(t, http.StatusUnauthorized, p.Status)
	assert.Equal(t, apierrors.AuthSessionUnauthorized.Message, p.Detail)
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

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, apierrors.AuthPermissionInstanceAdminRequired.Code, p.Type)
	assert.Equal(t, http.StatusText(http.StatusForbidden), p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, apierrors.AuthPermissionInstanceAdminRequired.Message, p.Detail)
}

// TestWriteSpecErrorByCode_UnknownCodeFallsThrough verifies that a
// code not in [codeSpec] still produces an RFC 9457 envelope built
// from the supplied tuple.
func TestWriteSpecErrorByCode_UnknownCodeFallsThrough(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)

	writeSpecErrorByCode(rec, req, http.StatusTeapot, "FUTURE.UNKNOWN.CODE", "future message")

	require.Equal(t, http.StatusTeapot, rec.Code)
	var p problemBodyDecoded
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

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "AUTH.PERMISSION.INSTANCE_ADMIN_REQUIRED", p.Type)
	assert.Equal(t, "Forbidden", p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.NotEmpty(t, p.Detail)
}

// TestRequireInstanceAdmin_EmitsRFC9457OnMissingActor is the 401
// counterpart: the actor extractor reports no actor so the
// middleware short-circuits with AUTH.SESSION.UNAUTHORIZED.
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

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "AUTH.SESSION.UNAUTHORIZED", p.Type)
	assert.Equal(t, "Unauthorized", p.Title)
	assert.Equal(t, http.StatusUnauthorized, p.Status)
}

// TestRequireWorkspaceRole_EmitsRFC9457OnDeny verifies that the
// non-shared workspace-role middleware emits the canonical RFC 9457
// problem+json envelope on failure rather than a bare
// `{"code":"...","message":"..."}` payload.
func TestRequireWorkspaceRole_EmitsRFC9457OnDeny(t *testing.T) {
	t.Parallel()

	mw := RequireWorkspaceRole(WorkspaceRoleAdmin)

	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler must not run when role is below minimum")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workspaces/x", nil)
	ctx := context.WithValue(req.Context(), ctxKeyWorkspaceID, uint32(1))
	ctx = context.WithValue(ctx, ctxKeyWorkspaceRole, WorkspaceRoleMember)
	mw(final).ServeHTTP(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "WS.MEMBER.ROLE_DENIED", p.Type, "type must be the canonical error code, not a bare 'code' field")
	assert.Equal(t, "Forbidden", p.Title)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.NotEmpty(t, p.Detail)
}
