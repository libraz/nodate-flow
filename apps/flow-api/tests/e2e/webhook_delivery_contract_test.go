package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/webhook"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// capturedDelivery is one POST a subscriber's endpoint received.
type capturedDelivery struct {
	body   []byte
	header http.Header
}

// captureTarget stands in for a subscriber's endpoint and records every
// request it is given, so the assertions can be made against what was
// actually put on the wire rather than against what the worker meant to
// send.
type captureTarget struct {
	*httptest.Server
	mu   sync.Mutex
	hits []capturedDelivery
}

func newCaptureTarget(t *testing.T) *captureTarget {
	t.Helper()
	ct := &captureTarget{}
	ct.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ct.mu.Lock()
		ct.hits = append(ct.hits, capturedDelivery{body: body, header: r.Header.Clone()})
		ct.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ct.Close)
	return ct
}

func (c *captureTarget) received() []capturedDelivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedDelivery(nil), c.hits...)
}

// webhookFixture is a workspace with one subscription and one queued
// delivery for an event that targets a real task.
type webhookFixture struct {
	target          *captureTarget
	subInternalID   uint32
	subSecret       string
	deliveryPublic  types.PublicID
	eventPublicID   types.PublicID
	taskPublicID    string
	subscriptionURL string
	worker          *webhook.Worker
}

// newWebhookFixture creates the subscription over REST (so the row has
// production shape), inserts an events row bound to a real task, and
// drives the worker's fan-out hook until the delivery row exists.
func newWebhookFixture(ctx context.Context, t *testing.T, tt *helpers.TestTenant, eventTypes string) *webhookFixture {
	t.Helper()

	target := newCaptureTarget(t)
	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	var created struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost, wsURL+"/webhooks", tt.AccessToken, map[string]any{
		"url":         target.URL,
		"description": "delivery contract e2e",
		"eventTypes":  json.RawMessage(eventTypes),
	}, &created)
	require.NotEmpty(t, created.Webhook.ID)

	taskPublicID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "webhook delivery contract")

	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, tt.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, tt.UserPublicID)
	subInternalID := lookupSubscriptionInternalID(ctx, t, testDB, created.Webhook.ID)
	taskInternalID := lookupTaskInternalID(ctx, t, testDB, taskPublicID)

	var secret string
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT secret FROM webhook_subscriptions WHERE id = ?`, subInternalID).Scan(&secret))

	// The events row is inserted directly so the test owns the event's
	// public id, its occurred_at and — the point of the exercise — the
	// task the event targets.
	eventPubID := types.New()
	res, err := helpers.ExecRetry(ctx, testDB, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, type, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, 'task.created', JSON_OBJECT('taskId', ?), NOW(3))
	`, eventPubID, wsInternalID, taskInternalID, ownerInternalID, taskPublicID)
	require.NoError(t, err)
	eventLastID, err := res.LastInsertId()
	require.NoError(t, err)

	w := webhook.NewWorker(testDB, generated.New(testDB))
	w.Hook()(ctx, wsInternalID, "task.created", uint64(eventLastID)) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	require.Eventually(t, func() bool {
		return webhookDeliveryCountForSubscription(ctx, t, testDB, subInternalID) >= 1
	}, 5*time.Second, 25*time.Millisecond,
		"the fan-out hook must queue a delivery for the matching subscription")

	var deliveryPubID types.PublicID
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT public_id FROM webhook_deliveries WHERE subscription_id = ? ORDER BY id DESC LIMIT 1`,
		subInternalID).Scan(&deliveryPubID))

	// The row is queued with next_retry_at = now, and the claim wants it
	// strictly due; a second of backdating removes the DATETIME(3)
	// truncation race rather than making the test wait one out.
	_, err = testDB.ExecContext(ctx,
		`UPDATE webhook_deliveries SET next_retry_at = NOW() - INTERVAL 1 SECOND WHERE public_id = ?`,
		deliveryPubID)
	require.NoError(t, err)

	return &webhookFixture{
		target:          target,
		subInternalID:   subInternalID,
		subSecret:       secret,
		deliveryPublic:  deliveryPubID,
		eventPublicID:   eventPubID,
		taskPublicID:    taskPublicID,
		subscriptionURL: wsURL + "/webhooks/" + created.Webhook.ID,
		worker:          w,
	}
}

// TestWebhookDeliveryIdentifiesTheResource is the payload contract. A
// body of {eventType, workspaceId, occurredAt} tells a receiver that
// something changed and gives it no way to find out what: not the event,
// not the row, and no id to fetch either with. Everything asserted here
// is what makes an outbound delivery usable by an integration.
//
// The second half is the invariant that matters more: not one internal
// sequential id may appear, in the body or in a header. The ids the
// worker has in hand at this point — the workspace, the task, the
// subscription, the delivery, the event — are all internal, and the only
// identifiers a subscriber may see are public_ids.
func TestWebhookDeliveryIdentifiesTheResource(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()
	tt := newTenant(t)
	fx := newWebhookFixture(ctx, t, tt, `["task.created"]`)

	fx.worker.ProcessOnceForSubscription(ctx, fx.subInternalID)

	hits := fx.target.received()
	require.Len(t, hits, 1, "the subscriber's endpoint must have been POSTed exactly once")
	got := hits[0]

	var payload struct {
		EventID     string `json:"eventId"`
		EventType   string `json:"eventType"`
		DeliveryID  string `json:"deliveryId"`
		WorkspaceID string `json:"workspaceId"`
		OccurredAt  int64  `json:"occurredAt"`
		Resources   []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal(got.body, &payload), "body=%s", string(got.body))

	require.Equal(t, fx.eventPublicID.String(), payload.EventID,
		"the body must name the event it reports, so a receiver can fetch it and can dedupe retries")
	require.Equal(t, fx.deliveryPublic.String(), payload.DeliveryID,
		"the body must name the delivery, so a receiver can correlate it with the X-Nodate-Delivery header")
	require.Equal(t, tt.WorkspacePublicID, payload.WorkspaceID)
	require.Equal(t, "task.created", payload.EventType)
	require.NotZero(t, payload.OccurredAt)
	require.Len(t, payload.Resources, 1,
		"the body must name the resource the event targets; body=%s", string(got.body))
	require.Equal(t, "task", payload.Resources[0].Type)
	require.Equal(t, fx.taskPublicID, payload.Resources[0].ID,
		"the resource id must be the task's public_id")

	assertNoInternalIDs(t, got)
}

// assertNoInternalIDs walks a delivered request and fails on anything
// that could be an internal sequential id.
//
// It asserts shape rather than searching for the specific numbers this
// test happens to have allocated: every id-shaped key must hold a UUID
// string, and the timestamp is the only number a delivery may contain.
// A search for "does 4271 appear anywhere" would both miss a leak under
// a key nobody thought of and match a UUID that happens to contain the
// digits.
func assertNoInternalIDs(t *testing.T, got capturedDelivery) {
	t.Helper()

	var tree map[string]any
	require.NoError(t, json.Unmarshal(got.body, &tree))
	walkJSON(t, "", tree)

	// The only nodate headers a subscriber sees. An internal id smuggled
	// into a new one would land here.
	want := map[string]bool{
		"X-Nodate-Signature": true,
		"X-Nodate-Timestamp": true,
		"X-Nodate-Event":     true,
		"X-Nodate-Delivery":  true,
	}
	for name, values := range got.header {
		if len(name) < len("X-Nodate-") || name[:len("X-Nodate-")] != "X-Nodate-" {
			continue
		}
		require.Truef(t, want[name], "unexpected delivery header %s: %v", name, values)
	}
	_, err := types.Parse(got.header.Get("X-Nodate-Delivery"))
	require.NoError(t, err, "X-Nodate-Delivery must be a public_id, got %q",
		got.header.Get("X-Nodate-Delivery"))
}

// walkJSON enforces the two rules on every node of a delivery body.
func walkJSON(t *testing.T, path string, node any) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if payloadscan.IsIDKey(key) {
				s, ok := child.(string)
				require.Truef(t, ok,
					"%s holds %T; every identifier on the wire is a public_id (UUID v7)", childPath, child)
				_, err := types.Parse(s)
				require.NoErrorf(t, err, "%s = %q is not a UUID", childPath, s)
			}
			walkJSON(t, childPath, child)
		}
	case []any:
		for i, child := range v {
			walkJSON(t, path+"["+strconv.Itoa(i)+"]", child)
		}
	case float64:
		require.Equalf(t, "occurredAt", path,
			"%s is a number (%v); the timestamp is the only number a delivery may carry, "+
				"anything else numeric is an internal sequential id", path, v)
	}
}

// TestWebhookDeliverySignatureCoversTheTimestamp is the replay guard on
// the wire. Signing the body alone left a captured delivery valid
// forever: a receiver checking the signature had nothing to tell a
// replay from the original.
func TestWebhookDeliverySignatureCoversTheTimestamp(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()
	tt := newTenant(t)
	fx := newWebhookFixture(ctx, t, tt, `["task.created"]`)

	fx.worker.ProcessOnceForSubscription(ctx, fx.subInternalID)

	hits := fx.target.received()
	require.Len(t, hits, 1)
	got := hits[0]

	timestamp := got.header.Get("X-Nodate-Timestamp")
	require.NotEmpty(t, timestamp, "a delivery must carry the instant it was signed")
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	require.NoError(t, err, "X-Nodate-Timestamp must be Unix seconds, got %q", timestamp)
	require.InDelta(t, time.Now().Unix(), ts, 300,
		"the signing timestamp must be the dispatch instant, or a receiver's skew check rejects every delivery")

	signature := got.header.Get("X-Nodate-Signature")
	require.NotEmpty(t, signature)
	require.Equal(t, v0Signature(fx.subSecret, timestamp, got.body), signature,
		"the signature must be HMAC-SHA256 over \"v0:\"+timestamp+\":\"+body")

	// The replay: the captured body and signature, presented under a
	// timestamp a receiver's clock would still accept. If the timestamp
	// were outside the signed material this would verify.
	replayTS := strconv.FormatInt(ts+3600, 10)
	require.NotEqual(t, v0Signature(fx.subSecret, replayTS, got.body), signature,
		"replaying a captured delivery under a fresh timestamp must break the signature")
}

// v0Signature is the receiver's half of the outbound signing scheme,
// written from the documented format rather than shared with the
// implementation so the test can disagree with it.
func v0Signature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookPausedSubscriptionStopsQueuedDeliveries covers the half of
// "stop sending" that a subscriber can actually observe. Pausing a
// subscription used to leave everything already queued to be delivered
// anyway, so from the receiving end the switch did nothing for as long
// as the backlog lasted.
func TestWebhookPausedSubscriptionStopsQueuedDeliveries(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()
	tt := newTenant(t)
	fx := newWebhookFixture(ctx, t, tt, `["task.created"]`)

	// Pause after the delivery is queued: this is the ordering that
	// matters, and the one the old code got wrong.
	var toggled struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPatch, fx.subscriptionURL+"/toggle", tt.AccessToken,
		map[string]any{"isActive": false}, &toggled)
	require.True(t, toggled.Ok)

	fx.worker.ProcessOnceForSubscription(ctx, fx.subInternalID)

	require.Empty(t, fx.target.received(),
		"a paused subscription must receive nothing, including deliveries queued before the pause")

	status, reason := deliveryOutcome(ctx, t, fx.deliveryPublic)
	require.Equal(t, "dead", status,
		"a delivery for a paused subscription must leave the queue instead of being re-claimed every tick")
	require.Contains(t, reason, "paused",
		"the recorded reason must distinguish this from an exhausted retry budget")
}

// TestWebhookDeletedSubscriptionStopsQueuedDeliveries is the same
// contract for the harder switch: deleting a subscription is the one
// action a workspace has left when an integration misbehaves, and it has
// to take effect for the backlog too.
func TestWebhookDeletedSubscriptionStopsQueuedDeliveries(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ctx := context.Background()
	tt := newTenant(t)
	fx := newWebhookFixture(ctx, t, tt, `["task.created"]`)

	doJSON(t, http.MethodDelete, fx.subscriptionURL, tt.AccessToken, nil, nil)

	fx.worker.ProcessOnceForSubscription(ctx, fx.subInternalID)

	require.Empty(t, fx.target.received(),
		"a deleted subscription must receive nothing, including deliveries queued before the deletion")

	status, reason := deliveryOutcome(ctx, t, fx.deliveryPublic)
	require.Equal(t, "dead", status)
	require.Contains(t, reason, "deleted")
}

// deliveryOutcome reads the terminal state a delivery row settled on.
func deliveryOutcome(ctx context.Context, t *testing.T, publicID types.PublicID) (string, string) {
	t.Helper()
	var (
		status string
		body   sql.NullString
	)
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT status, response_body FROM webhook_deliveries WHERE public_id = ?`,
		publicID).Scan(&status, &body))
	return status, body.String
}
