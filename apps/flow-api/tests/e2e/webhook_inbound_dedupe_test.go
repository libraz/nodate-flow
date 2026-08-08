package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	gh "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/github"
	goog "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/google"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// headerGoogleMessageNumber is Google's per-delivery counter. It is spelled
// out here rather than imported so the test states the wire contract
// independently of the handler's own constant.
const headerGoogleMessageNumber = "X-Goog-Message-Number"

// countingEnqueuer records every judge dispatch so a test can assert a
// duplicate delivery does not wake the judge a second time.
type countingEnqueuer struct {
	mu  sync.Mutex
	ids []int64
}

func (c *countingEnqueuer) EnqueueForSignal(_ context.Context, signalID int64, _ uint32, _ signalkinds.Kind) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, signalID)
	return nil
}

func (c *countingEnqueuer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ids)
}

// webhookResponse is the JSON body the inbound webhook handlers return.
type webhookResponse struct {
	ID        string `json:"id"`
	Duplicate bool   `json:"duplicate"`
}

// callWebhook drives a chi-level webhook handler directly. The inbound
// webhook routes are unauthenticated and resolve their workspace from the
// sender identity on the delivery, so each test maps its own sender to
// its own tenant instead of perturbing the shared server's configuration.
func callWebhook(t *testing.T, h http.HandlerFunc, req *http.Request) (int, webhookResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, req)
	var out webhookResponse
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
			"webhook response is not JSON: %s", rec.Body.String())
	}
	return rec.Code, out
}

// countSignals returns how many signals rows the tenant holds for the
// given source and external_id. Every assertion in this file is scoped to
// the test's own tenant and its own external ids, so the parallel suite
// sharing one MySQL instance cannot perturb it.
func countSignals(t *testing.T, workspacePublicID, source, externalID string) int {
	t.Helper()
	var n int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND source = ?
		   AND external_id = ?`,
		workspacePublicID, source, externalID).Scan(&n)
	require.NoError(t, err)
	return n
}

// mapWebhookSource claims an external webhook sender for the tenant's
// workspace by writing the routing row directly, so a test that drives a
// chi handler in-process does not have to stand up an admin session
// first. TestWebhookDeliveriesRouteToTheMappedWorkspace covers the REST
// surface that operators actually use.
func mapWebhookSource(t *testing.T, workspacePublicID, provider, externalKey string) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO integration_source_mappings (public_id, workspace_id, provider, external_key, label)
		 VALUES (UUID_TO_BIN(UUID(), 0),
		         (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)),
		         ?, ?, ?)`,
		workspacePublicID, provider, externalKey, "test "+provider+" source")
	require.NoError(t, err)
}

// TestGithubWebhookDedupesByDeliveryID verifies that a GitHub redelivery
// (identical X-GitHub-Delivery) lands as one signals row, returns the
// existing row's public id, and does not wake the judge a second time.
//
// GitHub redelivers on any non-2xx and offers a manual redeliver button,
// so the delivery id has always been on external_id — but the collided
// INSERT was not detected, and the handler answered with a freshly minted
// public id that matched no persisted row while still dispatching a judge
// run for it.
func TestGithubWebhookDedupesByDeliveryID(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "github-webhook-secret-for-dedupe-test" //#nosec G101 -- synthetic test fixture, never a real secret
	enq := &countingEnqueuer{}
	handler := signals.HandleGithubWebhook(signals.Deps{
		DB:              testDB,
		Queries:         generated.New(testDB),
		GhWebhookSecret: secret,
		JudgeEnqueuer:   enq,
	})

	// GitHub routes on repository.id; a per-test random id keeps the
	// instance-wide UNIQUE (provider, external_key) claim collision-free
	// under the parallel suite.
	repoSuffix, err := strconv.ParseInt(randomHex(6), 16, 64)
	require.NoError(t, err)
	repoID := repoSuffix + 1
	mapWebhookSource(t, tt.WorkspacePublicID, "github", strconv.FormatInt(repoID, 10))

	body, err := json.Marshal(map[string]any{
		"action": "opened",
		"repository": map[string]any{
			"id":        repoID,
			"full_name": "acme/widgets",
		},
	})
	require.NoError(t, err)

	deliveryID := "gh-delivery-" + randomHex(8)
	deliver := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
		req.Header.Set(gh.SignatureHeader, gh.Sign(body, secret))
		req.Header.Set(gh.EventHeader, "issues")
		req.Header.Set("X-GitHub-Delivery", deliveryID)
		return req
	}

	status, first := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first.ID)
	require.False(t, first.Duplicate, "the first delivery is a genuine insert")
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID))
	require.Equal(t, 1, enq.count(), "the first delivery must reach the judge")

	// GitHub's redelivery of the same delivery id.
	status, second := callWebhook(t, handler, deliver())
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so GitHub stops retrying")
	require.True(t, second.Duplicate, "the redelivery must be reported as a duplicate")
	require.Equal(t, first.ID, second.ID,
		"the redelivery must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "github", deliveryID),
		"a GitHub redelivery must not create a second signals row")
	require.Equal(t, 1, enq.count(), "a duplicate delivery must not enqueue a second judge run")
}

// TestSlackWebhookDedupesByEventID verifies that a Slack event redelivery
// (identical top-level `event_id`) lands as one signals row, returns the
// existing row's public id, and does not wake the judge a second time.
//
// Slack retries an event up to three times when the receiver is slow or
// errors, so without event_id on external_id the (workspace_id, source,
// external_id) unique key never engages and one message becomes several
// signals plus several judge runs.
func TestSlackWebhookDedupesByEventID(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const secret = "slack-signing-secret-for-dedupe-test"
	enq := &countingEnqueuer{}
	handler := signals.HandleSlackWebhook(signals.Deps{
		DB:                 testDB,
		Queries:            generated.New(testDB),
		SlackSigningSecret: secret,
		JudgeEnqueuer:      enq,
	})

	teamID := "T" + strings.ToUpper(randomHex(4))
	mapWebhookSource(t, tt.WorkspacePublicID, "slack", teamID)

	eventID := "Ev" + randomHex(8)
	body, err := json.Marshal(map[string]any{
		"type":     "event_callback",
		"event_id": eventID,
		"team_id":  teamID,
		"event": map[string]any{
			"type": "app_mention",
			"text": "hello",
		},
	})
	require.NoError(t, err)

	sign := func() *http.Request {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
		req.Header.Set(sl.TimestampHeader, ts)
		req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, secret))
		return req
	}

	status, first := callWebhook(t, handler, sign())
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, first.ID)
	require.False(t, first.Duplicate, "the first delivery is a genuine insert")
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "slack", eventID))
	require.Equal(t, 1, enq.count(), "the first delivery must reach the judge")

	// Slack's retry of the same event.
	status, second := callWebhook(t, handler, sign())
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so Slack stops retrying")
	require.True(t, second.Duplicate, "the redelivery must be reported as a duplicate")
	require.Equal(t, first.ID, second.ID,
		"the redelivery must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "slack", eventID),
		"a Slack retry must not create a second signals row")
	require.Equal(t, 1, enq.count(), "a duplicate delivery must not enqueue a second judge run")
}

// TestGoogleWebhookRecordsEveryDeliveryOnAChannel is the core regression
// for the Drive push receiver: a watch channel's id is fixed for the
// channel's whole lifetime, so keying external_id on it alone let the
// first notification in and silently discarded every later one while
// still answering 202. Pairing the channel id with the per-delivery
// message number keeps each notification distinct and still dedupes
// Google's retries of one notification.
func TestGoogleWebhookRecordsEveryDeliveryOnAChannel(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const channelToken = "google-channel-token-for-dedupe-test"
	enq := &countingEnqueuer{}
	handler := signals.HandleGoogleWebhook(signals.Deps{
		DB:                 testDB,
		Queries:            generated.New(testDB),
		GoogleChannelToken: channelToken,
		JudgeEnqueuer:      enq,
	})

	channelID := "chan-" + randomHex(8)
	mapWebhookSource(t, tt.WorkspacePublicID, "google", channelID)
	push := func(messageNumber, resourceState string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/google",
			bytes.NewReader([]byte(`{"kind":"drive#change"}`)))
		req.Header.Set(goog.HeaderChannelToken, channelToken)
		req.Header.Set(goog.HeaderChannelID, channelID)
		req.Header.Set(headerGoogleMessageNumber, messageNumber)
		req.Header.Set(goog.HeaderResourceState, resourceState)
		return req
	}

	status, sync := callWebhook(t, handler, push("1", "sync"))
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, sync.ID)
	require.False(t, sync.Duplicate)

	// A second, different notification on the SAME channel. This is the
	// delivery the previous key dropped.
	status, change := callWebhook(t, handler, push("2", "update"))
	require.Equal(t, http.StatusAccepted, status)
	require.False(t, change.Duplicate,
		"a different message number on the same channel is a new notification, not a duplicate")
	require.NotEqual(t, sync.ID, change.ID,
		"two notifications on one channel must be two distinct signals")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":1"))
	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":2"),
		"the second notification on the channel must be recorded, not swallowed")
	require.Equal(t, 2, enq.count(), "both notifications must reach the judge")

	// Google's retry of notification 2.
	status, retry := callWebhook(t, handler, push("2", "update"))
	require.Equal(t, http.StatusAccepted, status,
		"a duplicate must still be acknowledged so Google stops retrying")
	require.True(t, retry.Duplicate, "the retry must be reported as a duplicate")
	require.Equal(t, change.ID, retry.ID,
		"the retry must return the existing row's public id, not a freshly minted one")

	require.Equal(t, 1, countSignals(t, tt.WorkspacePublicID, "google", channelID+":2"),
		"a Google retry must not create a second signals row")
	require.Equal(t, 2, enq.count(), "a duplicate delivery must not enqueue a third judge run")
}
