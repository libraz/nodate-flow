package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mutationCase describes one state-changing endpoint that answers about
// a resource the caller named by public id.
//
// Both halves are required. Asserting only that a missing id gives 404
// would be satisfied by an endpoint that refuses everything, and
// asserting only the happy path is what the code did before — it always
// said ok. Each case therefore runs the same request twice: once against
// an id that is well-formed but names nothing, and once against a
// resource this test just created.
type mutationCase struct {
	// name identifies the endpoint in failure output.
	name string
	// method and the two paths. missing must be a syntactically valid
	// public id so the request reaches the query rather than being
	// rejected by path validation.
	method string
	// setup returns (existingPath, missingPath) and, when the endpoint
	// takes a body, the body to send.
	setup func(t *testing.T, tt *helpers.TestTenant) (existing, missing string, body any)
	// notFound is the problem type the missing-id request must produce.
	notFound string
}

// unknownID returns a syntactically valid public id that names nothing.
func unknownID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}

func createTimeboxFor(t *testing.T, tt *helpers.TestTenant, name string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeboxes",
		tt.AccessToken, map[string]any{
			"name":     name,
			"startsOn": "2026-01-05",
			"endsOn":   "2026-01-09",
		}, &out)
	require.NotEmpty(t, out.ID)
	return out.ID
}

// mutationCases is the registry. It is a list rather than a set of
// hand-written test functions because the defect it guards against was
// never one endpoint: the same "ignore the affected-row count and answer
// ok" appeared independently across more than ten of them. A registry
// makes adding the eleventh cost one entry, and makes the omission
// visible when somebody adds an endpoint and not an entry.
func mutationCases() []mutationCase {
	return []mutationCase{
		{
			name:     "DELETE timebox",
			method:   http.MethodDelete,
			notFound: "TIMEBOX.TIMEBOX.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes/"
				return base + createTimeboxFor(t, tt, "Delete me"), base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE timebox task",
			method:   http.MethodDelete,
			notFound: "TIMEBOX.TASK.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				tb := createTimeboxFor(t, tt, "Remove task from me")
				task := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Timebox member")
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes/" + tb + "/tasks/"
				doJSON(t, http.MethodPost,
					testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeboxes/"+tb+"/tasks",
					tt.AccessToken, map[string]any{"taskId": task}, nil)
				// The missing case uses a real task that was never added
				// to this timebox: the link is what must be absent, and a
				// task id that does not resolve would fail earlier for a
				// different reason.
				other := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Not in the timebox")
				return base + task, base + other, nil
			},
		},
		{
			name:     "DELETE mcp token",
			method:   http.MethodDelete,
			notFound: "MCP.TOKEN.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/me/mcp-tokens/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
					tt.AccessToken, map[string]any{"name": "revoke me", "scopes": []string{"read:workspace"}}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE page",
			method:   http.MethodDelete,
			notFound: "PAGE.PAGE.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/pages",
					tt.AccessToken, map[string]any{"title": "Delete me"}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE webhook subscription",
			method:   http.MethodDelete,
			notFound: "WEBHOOK.SUBSCRIPTION.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/webhooks/"
				var out struct {
					Webhook struct {
						ID string `json:"id"`
					} `json:"webhook"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/webhooks",
					tt.AccessToken, map[string]any{
						"url":         "https://example.test/hook",
						"description": "delete me",
						"eventTypes":  []string{"task.created"},
					}, &out)
				require.NotEmpty(t, out.Webhook.ID)
				return base + out.Webhook.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE label",
			method:   http.MethodDelete,
			notFound: "WS.LABEL.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/labels/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/labels",
					tt.AccessToken, map[string]any{"name": "delete-me-" + unknownID(t)[:8]}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE lens",
			method:   http.MethodDelete,
			notFound: "WS.LENS.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/lenses",
					tt.AccessToken, map[string]any{
						"name":      "Delete me",
						"filter":    map[string]any{},
						"sort":      []any{},
						"isDefault": false,
					}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE dashboard widget",
			method:   http.MethodDelete,
			notFound: "WS.DASHBOARD_WIDGET.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/dashboard/widgets",
					tt.AccessToken, map[string]any{
						"widgetType": "task_summary",
						"title":      "Delete me",
						"config":     map[string]any{},
						"positionX":  0,
						"positionY":  0,
						"width":      1,
						"height":     1,
					}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "DELETE favorite",
			method:   http.MethodDelete,
			notFound: "WS.FAVORITE.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				base := testServerURL + "/me/favorites/"
				q := "?workspaceId=" + tt.WorkspacePublicID
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/me/favorites", tt.AccessToken, map[string]any{
					"workspaceId": tt.WorkspacePublicID,
					"targetType":  "project",
					"targetId":    tt.ProjectPublicID,
				}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID + q, base + unknownID(t) + q, nil
			},
		},
		{
			name:   "DELETE task comment",
			method: http.MethodDelete,
			// The handler resolves the comment's author before deleting, and
			// that lookup is what answers for an id naming nothing. The
			// affected-row check behind it covers the narrower case of a
			// comment another request deleted a moment earlier.
			notFound: "WS.TASK.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				task := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Comment host")
				base := testServerURL + "/tasks/" + task + "/comments/"
				var out struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task+"/comments",
					tt.AccessToken, map[string]any{"body": "delete me"}, &out)
				require.NotEmpty(t, out.ID)
				return base + out.ID, base + unknownID(t), nil
			},
		},
		{
			name:     "POST inbox archive",
			method:   http.MethodPost,
			notFound: "WS.INBOX.NOT_FOUND",
			setup: func(t *testing.T, tt *helpers.TestTenant) (string, string, any) {
				var created struct {
					ID string `json:"id"`
				}
				doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
					"workspaceId": tt.WorkspacePublicID,
					"source":      "manual",
					"kind":        "manual",
				}, &created)
				require.NotEmpty(t, created.ID)
				q := "?workspaceId=" + tt.WorkspacePublicID
				return testServerURL + "/inbox/" + created.ID + "/archive" + q,
					testServerURL + "/inbox/" + unknownID(t) + "/archive" + q,
					nil
			},
		},
	}
}

// TestMutationsReportWhetherAnythingChanged runs the registry.
//
// The failure this pins is not cosmetic. An endpoint that answers ok
// without having changed a row also writes the audit entry and the
// timeline event that say it did, so the record of what happened
// disagrees with what happened — which is exactly the record somebody
// reads during an incident. The named case is revoking an MCP token: the
// UI showed a green toast while the token stayed valid.
//
// Assertions are per case and scoped to resources this test creates;
// nothing counts rows instance-wide, because the suite runs in parallel
// against a shared database.
func TestMutationsReportWhetherAnythingChanged(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	for _, tc := range mutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tt := newTenant(t)
			existing, missing, body := tc.setup(t, tt)

			status, raw := doJSONStatus(t, tc.method, missing, tt.AccessToken, body)
			require.Equalf(t, http.StatusNotFound, status,
				"%s against an id that names nothing must be 404, not a success that "+
					"writes an audit entry for something that never happened: %s",
				tc.name, string(raw))
			require.Equal(t, tc.notFound, problemType(t, raw))

			status, raw = doJSONStatus(t, tc.method, existing, tt.AccessToken, body)
			require.Truef(t, status >= 200 && status < 300,
				"%s against a resource that exists must still succeed; a guard that "+
					"rejects everything passes the check above and breaks the product: %s",
				tc.name, string(raw))

			// And the second time, having already changed the row, it must
			// report that there was nothing left to change.
			status, raw = doJSONStatus(t, tc.method, existing, tt.AccessToken, body)
			require.Equalf(t, http.StatusNotFound, status,
				"%s repeated on an already-removed resource must be 404: %s",
				tc.name, string(raw))
		})
	}
}

// TestMutationRegistryCoversTheAuditedEndpoints keeps the registry from
// quietly shrinking. The list below is the set of endpoints named in the
// review that produced this file; dropping one from mutationCases would
// remove its coverage without any test turning red.
func TestMutationRegistryCoversTheAuditedEndpoints(t *testing.T) {
	t.Parallel()

	have := map[string]bool{}
	for _, tc := range mutationCases() {
		have[tc.name] = true
	}
	for _, want := range []string{
		"DELETE timebox",
		"DELETE timebox task",
		"DELETE mcp token",
		"DELETE page",
		"DELETE webhook subscription",
		"POST inbox archive",
	} {
		require.Truef(t, have[want],
			"%q lost its entry in mutationCases; it was one of the endpoints that "+
				"answered ok without changing anything", want)
	}
	require.GreaterOrEqual(t, len(mutationCases()), 10,
		fmt.Sprintf("the registry has shrunk to %d entries", len(mutationCases())))
}
