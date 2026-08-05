package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/webhook"
)

// seedDeliveringRow inserts a webhook delivery already stranded in the
// 'delivering' status, with updated_at backdated so it is past any
// reasonable threshold. This is the state a worker leaves behind when it
// is killed between claiming a row and finishing the POST.
func seedDeliveringRow(t *testing.T, workspacePublicID, subscriptionPublicID string, age time.Duration) types.PublicID {
	t.Helper()
	ctx := context.Background()

	wsID := internalWorkspaceID(t, testDB, workspacePublicID)
	var subID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM webhook_subscriptions WHERE public_id = UUID_TO_BIN(?, 0)`,
		subscriptionPublicID).Scan(&subID))

	pub := types.New()
	_, err := testDB.ExecContext(ctx, `
		INSERT INTO webhook_deliveries
			(public_id, workspace_id, subscription_id, event_type, event_public_id,
			 payload_json, status, attempts, max_attempts, next_retry_at, updated_at)
		VALUES (?, ?, ?, 'task.created', NULL, JSON_OBJECT(), 'delivering', 0, 6, NOW(3), ?)`,
		pub, wsID, subID, time.Now().UTC().Add(-age))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM webhook_deliveries WHERE public_id = ?`, pub)
	})
	return pub
}

func deliveryStatus(t *testing.T, pub types.PublicID) string {
	t.Helper()
	var status string
	require.NoError(t, testDB.QueryRow(
		`SELECT status FROM webhook_deliveries WHERE public_id = ?`, pub).Scan(&status))
	return status
}

// TestStrandedDeliveryIsRequeued is the regression for deliveries lost
// to a worker that stopped mid-batch.
//
// Claiming flips a row to 'delivering' and commits before the HTTP POST,
// so a deploy, an OOM kill or a panic in the middle of a batch leaves
// rows in a status no query selects again. Nothing retried them and
// nothing reported them: the subscriber's server was fine, the
// subscription was active, the event was in the log, and the delivery
// simply never arrived.
//
// The row must come back as retryable, and its attempt count must not
// have been spent on an attempt that never happened.
func TestStrandedDeliveryIsRequeued(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	var created struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/webhooks",
		tt.AccessToken, map[string]any{
			"url":         target.URL,
			"description": "stranded delivery reaper",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		}, &created)
	require.NotEmpty(t, created.Webhook.ID)

	stranded := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, time.Hour)
	fresh := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, 0)

	worker := webhook.NewWorker(testDB, generated.New(testDB))
	worker.StrandedAfter = time.Minute
	worker.RequeueStrandedForTest(context.Background())

	require.Equal(t, "failed", deliveryStatus(t, stranded),
		"a delivery abandoned in 'delivering' must return to the retry queue")

	// A row claimed moments ago is still being delivered by whoever
	// claimed it. Requeueing that one would send the same POST twice for
	// a request about to succeed, which is why the threshold exists.
	require.Equal(t, "delivering", deliveryStatus(t, fresh),
		"a delivery still within the threshold must be left alone")

	// The strand is charged against the retry budget. It is not a
	// completed attempt, so charging it is not strictly fair — but a row
	// whose payload kills the worker would otherwise be requeued
	// forever: attempts would never advance, and the attempts <
	// max_attempts filter that bounds every other retry path would never
	// bite. One of six attempts per strand puts that loop under the same
	// budget as an ordinary failure.
	var attempts int
	require.NoError(t, testDB.QueryRow(
		`SELECT attempts FROM webhook_deliveries WHERE public_id = ?`, stranded).Scan(&attempts))
	require.Equal(t, 1, attempts,
		"a strand must advance the retry budget so a row that keeps stranding eventually stops")
}

// TestRepeatedlyStrandedDeliveryStopsRetrying is the bound on the loop
// above. A delivery whose payload kills the worker every time strands
// every time; without a budget the reaper would resurrect it forever,
// and each round costs a crashed worker. Once the attempts are spent the
// row no longer matches the claim query and stays out of the queue.
func TestRepeatedlyStrandedDeliveryStopsRetrying(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	var created struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/webhooks",
		tt.AccessToken, map[string]any{
			"url":         target.URL,
			"description": "stranded budget",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		}, &created)

	pub := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, time.Hour)
	// Start one attempt short of the cap: the next strand exhausts it.
	_, err := testDB.Exec(
		`UPDATE webhook_deliveries SET attempts = max_attempts - 1 WHERE public_id = ?`, pub)
	require.NoError(t, err)

	worker := webhook.NewWorker(testDB, generated.New(testDB))
	worker.StrandedAfter = time.Minute
	worker.RequeueStrandedForTest(context.Background())

	var attempts, maxAttempts int
	require.NoError(t, testDB.QueryRow(
		`SELECT attempts, max_attempts FROM webhook_deliveries WHERE public_id = ?`, pub).
		Scan(&attempts, &maxAttempts))
	require.Equal(t, maxAttempts, attempts, "the strand must spend the last attempt")

	var claimable int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM webhook_deliveries
		WHERE public_id = ? AND status IN ('pending','failed')
		  AND next_retry_at <= NOW() AND attempts < max_attempts AND enabled = TRUE`,
		pub).Scan(&claimable))
	require.Zero(t, claimable,
		"a delivery that spent its budget on strands must not be claimed again")
}
