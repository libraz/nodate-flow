package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/webhook"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestWebhookRetryAndDeadLetter verifies the webhook delivery state
// machine for a failing target. The contract is:
//
//   - A delivery starts in 'pending' with attempts=0.
//   - Each failed attempt bumps attempts and rolls status to 'failed',
//     scheduling a future next_retry_at according to the exponential
//     backoff schedule (1s, 5s, 30s, ...).
//   - Once attempts reaches max_attempts the row is marked 'dead' and
//     next_retry_at is NULLed so the worker stops considering it.
//
// The test stands up a local httptest server that always responds 500,
// creates a webhook subscription pointing at it, seeds a single
// delivery row, and then drives the worker forward by repeatedly
// fast-forwarding next_retry_at to NOW() and calling
// ProcessOnceForSubscription. The subscription-scoped helper avoids
// touching pending rows from parallel tests.
func TestWebhookRetryAndDeadLetter(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)

	// Local 500-only target. Counts hits so we can assert the worker
	// actually fired the configured number of attempts.
	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "intentional failure", http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)

	// Create the subscription via REST so we exercise the real handler
	// code path for token issuance, validation, and persistence.
	var created struct {
		Webhook struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/webhooks",
		owner.AccessToken, map[string]any{
			"url":         target.URL,
			"description": "retry+dead-letter e2e",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		}, &created)
	require.NotEmpty(t, created.Webhook.ID)
	require.Equal(t, target.URL, created.Webhook.URL)

	ctx := context.Background()
	subInternalID := lookupSubscriptionInternalID(ctx, t, testDB, created.Webhook.ID)
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)

	// Seed a single delivery row. We bypass the eventbus hook because
	// the test server does not wire the production webhook worker, and
	// we want full control over which subscription the row targets.
	queries := generated.New(testDB)
	deliveryPubID := types.New()
	now := time.Now().UTC()
	_, err := queries.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
		PublicID:       deliveryPubID,
		WorkspaceID:    wsInternalID,
		SubscriptionID: subInternalID,
		EventType:      "task.created",
		PayloadJson:    json.RawMessage(`{"eventType":"task.created"}`),
		NextRetryAt:    sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)

	w := webhook.NewWorker(testDB, queries)

	// Drive deliver attempts. The worker bumps attempts on every call;
	// we fast-forward next_retry_at between iterations because the
	// production schedule is 1s/5s/30s/... and we do not want the test
	// to wait minutes. The cap matches max_attempts (default 6).
	const maxAttempts = 6
	for i := 0; i < maxAttempts+2; i++ {
		_, err := testDB.ExecContext(ctx,
			`UPDATE webhook_deliveries SET next_retry_at = NOW() WHERE public_id = ? AND status IN ('pending','failed')`,
			deliveryPubID)
		require.NoError(t, err)

		w.ProcessOnceForSubscription(ctx, subInternalID)

		status, _, _ := readDeliveryState(ctx, t, testDB, deliveryPubID)
		if status == "dead" {
			break
		}
	}

	// Final state: dead, attempts == max_attempts, next_retry_at NULL,
	// and the target was hit exactly max_attempts times.
	status, attempts, nextRetryAtValid := readDeliveryState(ctx, t, testDB, deliveryPubID)
	require.Equal(t, "dead", status,
		"after exhausting retries the delivery must transition to dead")
	require.Equal(t, uint8(maxAttempts), attempts,
		"delivery attempts must equal max_attempts after dead-lettering")
	require.False(t, nextRetryAtValid,
		"next_retry_at must be NULL once the delivery is dead so the worker stops re-fetching it")
	require.EqualValues(t, maxAttempts, atomic.LoadInt64(&hits),
		"the failing target must have been called exactly max_attempts times")

	// Sanity: the recorded http_status reflects the target's 500 response.
	var lastStatus int
	err = testDB.QueryRowContext(ctx,
		`SELECT http_status FROM webhook_deliveries WHERE public_id = ?`,
		deliveryPubID).Scan(&lastStatus)
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, lastStatus,
		"the recorded http_status must reflect the target's 500 response")
}

// lookupSubscriptionInternalID resolves a subscription's internal id
// from its public UUID. Mirrors lookupWorkspaceInternalID in
// notification_dedup_test.go.
func lookupSubscriptionInternalID(ctx context.Context, t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	pid, err := types.Parse(publicID)
	require.NoError(t, err)
	var id uint32
	err = db.QueryRowContext(ctx, `SELECT id FROM webhook_subscriptions WHERE public_id = ?`, pid).Scan(&id)
	require.NoError(t, err)
	return id
}

// readDeliveryState pulls the (status, attempts, next_retry_at_valid)
// triple for a delivery row, used for state-machine assertions.
func readDeliveryState(ctx context.Context, t *testing.T, db *sql.DB, publicID types.PublicID) (string, uint8, bool) {
	t.Helper()
	var (
		status      string
		attempts    uint8
		nextRetryAt sql.NullTime
	)
	err := db.QueryRowContext(ctx,
		`SELECT status, attempts, next_retry_at FROM webhook_deliveries WHERE public_id = ?`,
		publicID).Scan(&status, &attempts, &nextRetryAt)
	require.NoError(t, err)
	return status, attempts, nextRetryAt.Valid
}

// TestWebhookFanoutDedupAndOccurredAt is the H1 + M2 regression guard.
// The contract under test is:
//
//   - Firing the webhook hook twice for the same eventInternalID must
//     produce exactly one webhook_deliveries row per matching
//     subscription. Dedupe is enforced by the
//     (subscription_id, event_public_id) unique key combined with
//     INSERT IGNORE in CreateWebhookDelivery; the worker has to stamp
//     the source events.public_id on every row for the key to engage.
//   - The webhook payload's OccurredAt must come from the event row's
//     occurred_at, NOT from time.Now() at dispatch. Sleeping between
//     event creation and dispatch makes the difference observable: if
//     the worker still used time.Now() the payload would lag by the
//     sleep duration.
func TestWebhookFanoutDedupAndOccurredAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)

	// A no-op target is enough: the dedupe and OccurredAt assertions
	// read directly from webhook_deliveries, no HTTP delivery needed.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	// Create a subscription via REST so the row matches production
	// shape (token issuance, event_types JSON, enabled flag).
	var created struct {
		Webhook struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/webhooks",
		owner.AccessToken, map[string]any{
			"url":         target.URL,
			"description": "fanout dedupe + occurredAt e2e",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		}, &created)
	require.NotEmpty(t, created.Webhook.ID)

	ctx := context.Background()
	queries := generated.New(testDB)
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	subInternalID := lookupSubscriptionInternalID(ctx, t, testDB, created.Webhook.ID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)

	// Insert an event row directly so the test owns the occurred_at
	// instant and the events.id needed by the hook. The recorded
	// occurred_at is stamped two seconds in the past so a worker that
	// (incorrectly) uses time.Now() at dispatch will diverge from the
	// stored value by ~2s while a correct worker that propagates
	// events.occurred_at lands within the 1s slop tolerated below.
	// Backdating the row is preferred over an explicit sleep: the
	// dispatch path is observed via a real clock-skew gap without
	// adding wall-clock delay to the test.
	eventPubID := types.New()
	eventOccurredAt := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Second)
	res, err := helpers.ExecRetry(ctx, testDB, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, type, payload_json, occurred_at)
		VALUES (?, ?, NULL, ?, 'task.created', JSON_OBJECT(), ?)
	`, eventPubID, wsInternalID, ownerInternalID, eventOccurredAt)
	require.NoError(t, err)
	eventLastID, err := res.LastInsertId()
	require.NoError(t, err)
	eventInternalID := uint32(eventLastID) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Fire the worker's hook twice for the same eventInternalID. The
	// hook spawns goroutines, so we poll for the first row to land
	// before firing the second call to keep the dedupe deterministic.
	w := webhook.NewWorker(testDB, queries)
	hook := w.Hook()
	hook(ctx, wsInternalID, "task.created", eventInternalID)
	require.Eventually(t, func() bool {
		return webhookDeliveryCountForSubscription(ctx, t, testDB, subInternalID) >= 1
	}, 5*time.Second, 25*time.Millisecond,
		"first hook fire should land at least one delivery row")

	hook(ctx, wsInternalID, "task.created", eventInternalID)
	// Give the second goroutine a chance to attempt its insert; if
	// dedupe is broken this is when the duplicate row would appear.
	require.Never(t, func() bool {
		return webhookDeliveryCountForSubscription(ctx, t, testDB, subInternalID) > 1
	}, 1*time.Second, 25*time.Millisecond,
		"firing the same eventInternalID twice must dedupe to one delivery row")

	// Exactly one row, and it carries the source event's public_id.
	var (
		gotEventPubID types.PublicID
		gotPayload    json.RawMessage
		rowCount      int
	)
	err = testDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM webhook_deliveries
		WHERE subscription_id = ? AND event_type = 'task.created'
	`, subInternalID).Scan(&rowCount)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount,
		"dedupe key (subscription_id, event_public_id) must collapse repeats to 1 row")

	err = testDB.QueryRowContext(ctx, `
		SELECT event_public_id, payload_json
		FROM webhook_deliveries
		WHERE subscription_id = ? AND event_type = 'task.created'
		ORDER BY id DESC LIMIT 1
	`, subInternalID).Scan(&gotEventPubID, &gotPayload)
	require.NoError(t, err)
	require.Equal(t, eventPubID.String(), gotEventPubID.String(),
		"event_public_id must equal the source events.public_id (H1: required for dedupe)")

	// M2: payload OccurredAt must reflect the event's occurred_at,
	// not the dispatch time. Allow 1s of slop for clock truncation.
	var payload struct {
		EventType  string `json:"eventType"`
		OccurredAt int64  `json:"occurredAt"`
	}
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	require.Equal(t, "task.created", payload.EventType)
	driftSeconds := payload.OccurredAt - eventOccurredAt.Unix()
	require.GreaterOrEqual(t, driftSeconds, int64(-1),
		"payload OccurredAt drifted backwards beyond 1s tolerance: drift=%ds", driftSeconds)
	require.LessOrEqual(t, driftSeconds, int64(1),
		"payload OccurredAt must equal event occurred_at within 1s; drift=%ds proves the worker still uses time.Now()", driftSeconds)
}

// webhookDeliveryCountForSubscription returns the number of webhook
// delivery rows for a subscription, used as the dedupe assertion target.
func webhookDeliveryCountForSubscription(ctx context.Context, t *testing.T, db *sql.DB, subscriptionID uint32) int64 {
	t.Helper()
	var n int64
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE subscription_id = ?`,
		subscriptionID).Scan(&n)
	require.NoError(t, err)
	return n
}
