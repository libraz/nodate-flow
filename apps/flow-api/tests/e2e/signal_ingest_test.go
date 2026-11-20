package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

const slackMentionExpr = `{"op":"signal.received","arg":"app_mention"}`

// slackMentionDelivery builds a signed app_mention delivery with the given text.
func slackMentionDelivery(t *testing.T, secret, teamID, text string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type":     "event_callback",
		"event_id": "Ev" + randomHex(8),
		"team_id":  teamID,
		"event":    map[string]any{"type": "app_mention", "text": text},
	})
	require.NoError(t, err)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
	req.Header.Set(sl.TimestampHeader, ts)
	req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, secret))
	return req
}

// TestSlackSignalWithTaskMarkerSatisfiesConstraint pins that a Slack delivery
// whose text carries a tnk:<task uuid> marker binds to that task and
// satisfies its signal.received constraint with no explicit evaluate call.
func TestSlackSignalWithTaskMarkerSatisfiesConstraint(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "slack-signing-secret-for-marker-test"
	deps := webhookDeps()
	deps.SlackSigningSecret = secret
	handler := signals.HandleSlackWebhook(deps)

	teamID := "T" + strings.ToUpper(randomHex(4))
	mapWebhookSource(t, tt.WorkspacePublicID, "slack", teamID)

	taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Waits for a Slack mention")
	con := addTaskConstraint(t, tt.AccessToken, taskID, "signal", slackMentionExpr)
	// Let the constraint's own initial verdict append finish before the delivery.
	helpers.WaitReevalIdle(t, constraintWorkspaceID(t, tt))
	_, failedAt := readConstraintMarkers(t, con)
	require.True(t, failedAt.Valid, "with no signal bound the constraint must fail")

	status, resp := callWebhook(t, handler, slackMentionDelivery(t, secret, teamID, "status? tnk:"+taskID))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, resp.ID)

	waitConstraintSatisfied(t, con)
}

// TestSlackSignalMarkerFromOtherWorkspaceDoesNotBind pins that a marker
// naming a task in another workspace never binds and never satisfies that
// task's constraint.
func TestSlackSignalMarkerFromOtherWorkspaceDoesNotBind(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	sender := newTenant(t)
	owner := newTenant(t)

	const secret = "slack-signing-secret-for-cross-ws-test"
	deps := webhookDeps()
	deps.SlackSigningSecret = secret
	handler := signals.HandleSlackWebhook(deps)

	teamID := "T" + strings.ToUpper(randomHex(4))
	mapWebhookSource(t, sender.WorkspacePublicID, "slack", teamID)

	foreignTask := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Task in another workspace")
	foreignCon := addTaskConstraint(t, owner.AccessToken, foreignTask, "signal", slackMentionExpr)
	ownTask := createTask(t, sender.AccessToken, sender.ProjectPublicID, "Task in the sender workspace")
	ownCon := addTaskConstraint(t, sender.AccessToken, ownTask, "signal", slackMentionExpr)
	helpers.WaitReevalIdle(t, constraintWorkspaceID(t, owner))
	helpers.WaitReevalIdle(t, constraintWorkspaceID(t, sender))

	status, resp := callWebhook(t, handler, slackMentionDelivery(t, secret, teamID, "tnk:"+foreignTask))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, resp.ID, "the delivery itself is still recorded")

	helpers.WaitReevalIdle(t, constraintWorkspaceID(t, owner))
	helpers.WaitReevalIdle(t, constraintWorkspaceID(t, sender))

	satisfiedAt, failedAt := readConstraintMarkers(t, foreignCon)
	require.False(t, satisfiedAt.Valid, "a foreign marker must not satisfy the other workspace's constraint")
	require.True(t, failedAt.Valid)

	var bound int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND task_id IS NOT NULL`,
		sender.WorkspacePublicID).Scan(&bound))
	require.Zero(t, bound, "the signal must not bind to any task")

	satisfiedAt, _ = readConstraintMarkers(t, ownCon)
	require.False(t, satisfiedAt.Valid, "an unbound signal must not satisfy an unrelated task in its own workspace")
}
