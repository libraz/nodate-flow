package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestInferStateReadsOverdueInTheCallersZone holds GET infer-state to the
// caller's calendar day. A waiting task is proposed for review once it is
// overdue, and overdue means its due date is earlier than the caller's
// today — which differs from UTC's today for a zone far enough from UTC.
//
// No zone is behind UTC by more than twelve hours, so the fixture picks
// its zone by the clock: before 10:00 UTC a zone at UTC-11 is still on
// the previous day and a task due on that day is not overdue (UTC reads
// it as yesterday, so overdue); from 10:00 UTC a zone at UTC+14 is
// already on the next day and a task due on UTC's today is overdue there
// (UTC reads it as today, so not overdue).
func TestInferStateReadsOverdueInTheCallersZone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	now := time.Now().UTC()
	zoneName, wantProposal := "Pacific/Kiritimati", true
	if now.Hour() < 10 {
		zoneName, wantProposal = "Pacific/Pago_Pago", false
	}
	zone, err := time.LoadLocation(zoneName)
	require.NoError(t, err)
	dueOn := now.In(zone).Format("2006-01-02")
	if wantProposal {
		dueOn = now.Format("2006-01-02")
	}

	_, err = helpers.ExecRetry(context.Background(), testDB, "test seed: user timezone",
		`UPDATE users SET timezone = ? WHERE public_id = UUID_TO_BIN(?, 0)`, zoneName, tt.UserPublicID)
	require.NoError(t, err)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Infer-state zone",
		"dueOn":     dueOn,
	}, &task)
	require.NotEmpty(t, task.ID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/transitions", tt.AccessToken,
		map[string]any{"transition": "block"}, nil)

	var out struct {
		State    string `json:"state"`
		Proposal *struct {
			Transition string `json:"transition"`
		} `json:"proposal"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/infer-state", tt.AccessToken, nil, &out)
	require.Equal(t, "waiting", out.State)
	if wantProposal {
		require.NotNil(t, out.Proposal, "due %s is before today in %s, so the task is overdue", dueOn, zoneName)
		require.Equal(t, "submit", out.Proposal.Transition)
	} else {
		require.Nil(t, out.Proposal, "due %s is today in %s, so the task is not overdue", dueOn, zoneName)
	}
}
