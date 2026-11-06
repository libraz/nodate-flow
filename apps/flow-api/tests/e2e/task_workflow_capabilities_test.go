package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestCompletionPolicyEmptyRequiredConstraintsIsArray answers an empty
// required constraint set as [] on both the write and the read model.
func TestCompletionPolicyEmptyRequiredConstraintsIsArray(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	taskID := seedPublicTask(t, owner, "Empty required constraints")
	base := testServerURL + "/tasks/" + taskID

	for _, policy := range []string{"review_required", "manual"} {
		status, raw := doJSONStatus(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
			map[string]any{"policy": policy})
		require.Equalf(t, http.StatusOK, status, "policy %s: body=%s", policy, string(raw))
		requireJSONEmptyArray(t, raw, "requiredConstraintIds", "completion policy response for "+policy)

		status, raw = doJSONStatus(t, http.MethodGet, base+"/workflow", owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status, "workflow read: body=%s", string(raw))
		requireJSONEmptyArray(t, raw, "requiredConstraintIds", "workflow read after "+policy)
	}
}

// TestCompletionPolicyShrinkingRequiredConstraintsRequiresElevatedRole holds
// dropping a required constraint to the same role as lowering the policy,
// while adding one stays an editor action.
func TestCompletionPolicyShrinkingRequiredConstraintsRequiresElevatedRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	lead := seedProjectRoleMember(t, owner, "lead")
	taskID := seedPublicTask(t, owner, "Required constraint authority")
	base := testServerURL + "/tasks/" + taskID
	first := createApprovalConstraint(t, owner, taskID)
	second := createApprovalConstraint(t, owner, taskID)

	doJSON(t, http.MethodPost, base+"/completion-policy", editor.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{first}}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", editor.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{first, second}}, nil)
	require.ElementsMatch(t, []string{first, second}, workflowRequiredConstraintIDs(t, owner, taskID))

	for _, ids := range [][]string{{first}, {}} {
		status, raw := doJSONStatus(t, http.MethodPost, base+"/completion-policy", editor.AccessToken,
			map[string]any{"policy": "review_required", "requiredConstraintIds": ids})
		requireDenied(t, status, raw, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED",
			"an editor dropping a required constraint")
		require.ElementsMatch(t, []string{first, second}, workflowRequiredConstraintIDs(t, owner, taskID))
	}

	status, raw := doJSONStatus(t, http.MethodPost, base+"/completion-policy", editor.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{second, first}})
	require.Equalf(t, http.StatusOK, status, "reordering keeps the set: body=%s", string(raw))

	doJSON(t, http.MethodPost, base+"/completion-policy", lead.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{second}}, nil)
	require.Equal(t, []string{second}, workflowRequiredConstraintIDs(t, owner, taskID))
}

// TestWorkflowHandoffCanCancelMatchesCancelRule reports canCancel to exactly
// the callers the cancel decision accepts: the source holder, the requester,
// a project lead and a workspace admin.
func TestWorkflowHandoffCanCancelMatchesCancelRule(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	holder := seedProjectRoleMember(t, owner, "editor")
	otherEditor := seedProjectRoleMember(t, owner, "editor")
	recipient := seedProjectRoleMember(t, owner, "viewer")
	lead := seedProjectRoleMember(t, owner, "lead")
	admin := seedElevatedMember(t, owner)
	taskID := seedPublicTask(t, owner, "Handoff cancel capability")
	base := testServerURL + "/tasks/" + taskID

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": holder.UserPublicID, "role": "reviewer"}, nil)
	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/handoffs", admin.AccessToken,
		map[string]any{"role": "reviewer", "fromUserId": holder.UserPublicID, "toUserId": recipient.UserPublicID}, &handoff)
	require.NotEmpty(t, handoff.ID)

	for _, tc := range []struct {
		name   string
		caller *helpers.TestTenant
		want   bool
	}{
		{"source holder", holder, true},
		{"requesting workspace admin", admin, true},
		{"workspace owner", owner, true},
		{"project lead", lead, true},
		{"recipient", recipient, false},
		{"unrelated editor", otherEditor, false},
	} {
		require.Equalf(t, tc.want, workflowHandoffCanCancel(t, tc.caller, taskID, handoff.ID), "%s: canCancel", tc.name)
		if !tc.want {
			status, raw := doJSONStatus(t, http.MethodPost, base+"/handoffs/"+handoff.ID+"/decision", tc.caller.AccessToken,
				map[string]any{"decision": "cancel"})
			requireDenied(t, status, raw, http.StatusForbidden, "WS.TASK.ACCESS_DENIED", tc.name+" cancelling")
		}
	}

	doJSON(t, http.MethodPost, base+"/handoffs/"+handoff.ID+"/decision", holder.AccessToken,
		map[string]any{"decision": "cancel"}, nil)
	require.Equal(t, "cancelled", workflowHandoffStatus(t, owner, taskID, handoff.ID))
	require.False(t, workflowHandoffCanCancel(t, admin, taskID, handoff.ID), "a resolved handoff offers no withdrawal")
}

func createApprovalConstraint(t *testing.T, owner *helpers.TestTenant, taskID string) string {
	t.Helper()
	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", owner.AccessToken,
		map[string]any{
			"kind":       "approval",
			"expression": `{"op":"actor.has_role","arg":"approver"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	return constraint.ID
}

func workflowRequiredConstraintIDs(t *testing.T, reader *helpers.TestTenant, taskID string) []string {
	t.Helper()
	var workflow struct {
		RequiredConstraintIDs []string `json:"requiredConstraintIds"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/workflow", reader.AccessToken, nil, &workflow)
	return workflow.RequiredConstraintIDs
}

func workflowHandoffCanCancel(t *testing.T, reader *helpers.TestTenant, taskID, handoffID string) bool {
	t.Helper()
	var workflow struct {
		Handoffs []struct {
			ID        string `json:"id"`
			CanCancel *bool  `json:"canCancel"`
		} `json:"handoffs"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/workflow", reader.AccessToken, nil, &workflow)
	for _, item := range workflow.Handoffs {
		if item.ID == handoffID {
			require.NotNil(t, item.CanCancel, "handoff %s carries no canCancel", handoffID)
			return *item.CanCancel
		}
	}
	require.FailNowf(t, "handoff missing from workflow snapshot", "handoff=%s", handoffID)
	return false
}

func requireJSONEmptyArray(t *testing.T, raw []byte, field, label string) {
	t.Helper()
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &body), label)
	value, ok := body[field]
	require.Truef(t, ok, "%s: %s missing; body=%s", label, field, string(raw))
	require.JSONEqf(t, `[]`, string(value), "%s: %s", label, field)
}
