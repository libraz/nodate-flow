package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// auditListEntry mirrors a single entry returned by
// GET /workspaces/{wsId}/audit-logs.
type auditListEntry struct {
	PublicID          string  `json:"publicId"`
	ActorUserPublicID *string `json:"actorUserPublicId"`
	ActorDisplayName  *string `json:"actorDisplayName"`
	Action            string  `json:"action"`
	ResourceType      string  `json:"resourceType"`
	ResourcePublicID  *string `json:"resourcePublicId"`
	IPAddress         *string `json:"ipAddress"`
	UserAgent         *string `json:"userAgent"`
	OccurredAt        int64   `json:"occurredAt"`
}

// auditListResponse is the envelope returned by the audit list endpoint.
type auditListResponse struct {
	Total   int64            `json:"total"`
	Entries []auditListEntry `json:"entries"`
}

// TestAuditListReturnsWorkspaceCreateEntry verifies that the new
// workspace-scoped audit log endpoint returns the workspace.create row
// produced by newTenant, including a populated actor public id.
func TestAuditListReturnsWorkspaceCreateEntry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?limit=50&offset=0", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)

	require.Greater(t, resp.Total, int64(0), "total must be at least 1 for a fresh tenant")
	require.NotEmpty(t, resp.Entries, "entries must contain at least one row")

	var wsCreate *auditListEntry
	for i := range resp.Entries {
		e := resp.Entries[i]
		if e.Action == "workspace.create" && e.ResourceType == "workspace" {
			wsCreate = &resp.Entries[i]
			break
		}
	}
	require.NotNil(t, wsCreate, "workspace.create entry must be present")
	require.NotNil(t, wsCreate.ResourcePublicID)
	require.Equal(t, tt.WorkspacePublicID, *wsCreate.ResourcePublicID)
	require.NotNil(t, wsCreate.ActorUserPublicID, "actor public id must be populated")
	require.Equal(t, tt.UserPublicID, *wsCreate.ActorUserPublicID)
}

// TestAuditListFilterByAction verifies the action query filter narrows
// the result set to rows matching the exact action string.
func TestAuditListFilterByAction(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?action=project.create",
			testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)

	require.Greater(t, resp.Total, int64(0))
	require.NotEmpty(t, resp.Entries)
	for _, e := range resp.Entries {
		require.Equal(t, "project.create", e.Action,
			"action filter must restrict results to project.create only")
	}
}

// TestAuditListFilterByResourceType verifies the resourceType filter
// narrows the result set to rows of a specific kind.
func TestAuditListFilterByResourceType(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?resourceType=workspace",
			testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)

	require.NotEmpty(t, resp.Entries)
	for _, e := range resp.Entries {
		require.Equal(t, "workspace", e.ResourceType,
			"resourceType filter must restrict results to workspace rows only")
	}
}

// TestAuditListActorSearch verifies the actorSearch filter matches
// against the actor's display name.
func TestAuditListActorSearch(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// The tenant's display name contains a random hex suffix; search
	// by that suffix to prove the LIKE filter works.
	suffix := tt.DisplayName[len("Test User "):]
	var resp auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?actorSearch=%s",
			testServerURL, tt.WorkspacePublicID, url.QueryEscape(suffix)),
		tt.AccessToken, nil, &resp)

	require.NotEmpty(t, resp.Entries, "actor search by display-name suffix must find entries")
	for _, e := range resp.Entries {
		require.NotNil(t, e.ActorDisplayName)
		require.Contains(t, *e.ActorDisplayName, suffix)
	}
}

// TestAuditListPagination verifies that offset + limit move the window.
func TestAuditListPagination(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var page1 auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?limit=1&offset=0",
			testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &page1)

	require.Len(t, page1.Entries, 1, "limit=1 must return exactly one entry")
	// Total is independent of limit and must report the full count.
	require.Greater(t, page1.Total, int64(0))

	if page1.Total < 2 {
		return // cannot test page 2 with only one row
	}

	var page2 auditListResponse
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs?limit=1&offset=1",
			testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &page2)

	require.Len(t, page2.Entries, 1)
	require.Equal(t, page1.Total, page2.Total,
		"total must be stable across pages (computed against the filtered set, not the page)")
	require.NotEqual(t, page1.Entries[0].PublicID, page2.Entries[0].PublicID,
		"offset=1 must return a different row than offset=0")
}

// TestAuditListCrossTenantIsolation verifies that workspace A's admin
// cannot read workspace B's audit trail via the public id path param.
func TestAuditListCrossTenantIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	// Tenant A tries to list tenant B's audit log using tenant A's token.
	status, raw := doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/audit-logs", testServerURL, tenantB.WorkspacePublicID),
		tenantA.AccessToken, nil)

	// The workspace middleware refuses a non-member with 403
	// WS.WORKSPACE.ACCESS_DENIED. Asserting the exact pair is what
	// separates "the gate held" from "the audit reader crashed on a
	// workspace it could not resolve".
	requireDenied(t, status, raw, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"tenant A listing tenant B's audit log")
}
