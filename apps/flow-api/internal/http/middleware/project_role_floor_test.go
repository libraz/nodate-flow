package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// withProjectRole builds the request context RequireProjectMember and
// RequireProjectMemberByGlobalID leave behind, so a floor can be driven at a
// concrete role without a database.
func withProjectRole(role ProjectRole) context.Context {
	ctx := context.WithValue(context.Background(), ctxKeyProjectID, uint32(11))
	ctx = context.WithValue(ctx, ctxKeyProjectIDPublic, uuid.New())
	return context.WithValue(ctx, ctxKeyProjectRole, role)
}

// TestRequireProjectRoleAdmitsAndRefusesByRole pins which roles each project
// floor lets through.
//
// The chain tests in the router package prove a floor is mounted; they cannot
// tell one floor from another, because every one of them refuses a request
// whose ACL context was never resolved. This is the other half: the same
// middleware driven at each role, so lowering a floor from lead to editor —
// which would hand a project editor the power to grant roles — changes an
// answer here.
func TestRequireProjectRoleAdmitsAndRefusesByRole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		floor ProjectRole
		role  ProjectRole
		admit bool
	}{
		// The editor floor guards project metadata and the soft delete.
		{"editor floor refuses viewer", ProjectRoleEditor, ProjectRoleViewer, false},
		{"editor floor refuses commenter", ProjectRoleEditor, ProjectRoleCommenter, false},
		{"editor floor admits editor", ProjectRoleEditor, ProjectRoleEditor, true},
		{"editor floor admits lead", ProjectRoleEditor, ProjectRoleLead, true},
		{"editor floor admits elevated", ProjectRoleEditor, ProjectRoleElevated, true},
		{"editor floor refuses a member with no project role", ProjectRoleEditor, ProjectRoleNone, false},

		// The lead floor guards the member list. An editor is refused here
		// on purpose: granting roles is not an editing power.
		{"lead floor refuses viewer", ProjectRoleLead, ProjectRoleViewer, false},
		{"lead floor refuses commenter", ProjectRoleLead, ProjectRoleCommenter, false},
		{"lead floor refuses editor", ProjectRoleLead, ProjectRoleEditor, false},
		{"lead floor admits lead", ProjectRoleLead, ProjectRoleLead, true},
		{"lead floor admits elevated", ProjectRoleLead, ProjectRoleElevated, true},
		{"lead floor refuses a member with no project role", ProjectRoleLead, ProjectRoleNone, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reached := false
			handler := RequireProjectRole(tc.floor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPatch, "/projects/x", nil).
				WithContext(withProjectRole(tc.role))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if reached != tc.admit {
				t.Fatalf("role %q through the %q floor reached the handler = %v, want %v",
					tc.role, tc.floor, reached, tc.admit)
			}
			want := http.StatusForbidden
			if tc.admit {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Errorf("role %q through the %q floor = %d, want %d", tc.role, tc.floor, rec.Code, want)
			}
		})
	}
}

// TestRequireProjectRoleRefusesUnresolvedContext covers the failure mode a
// role table cannot: a request that never went through the resolving
// middleware, or one carrying a role string outside the enum. Neither is a
// role that outranks anything, so both are refused rather than read as the
// zero value.
func TestRequireProjectRoleRefusesUnresolvedContext(t *testing.T) {
	t.Parallel()

	contexts := map[string]context.Context{
		"no project resolved": context.Background(),
		"unparseable role":    withProjectRole(ProjectRole("not_a_real_role")),
	}

	for name, ctx := range contexts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reached := false
			handler := RequireProjectRole(ProjectRoleEditor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/projects/x", nil).WithContext(ctx))

			if reached {
				t.Error("the handler ran behind a project floor with no usable role")
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}
