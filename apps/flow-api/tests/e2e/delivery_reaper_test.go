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
// The row is written in the state the test needs in a single INSERT,
// attempts included. Seeding first and adjusting afterwards leaves a
// window in which a concurrent test's reaper — instance-wide by design
// — rescues the row before it is ready, and the adjustment then lands
// on a row that has already left 'delivering'.
func seedDeliveringRow(t *testing.T, workspacePublicID, subscriptionPublicID string, age time.Duration, attempts int) types.PublicID {
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
		VALUES (?, ?, ?, 'task.created', NULL, JSON_OBJECT(), 'delivering', ?, 6, NOW(3), ?)`,
		pub, wsID, subID, attempts, time.Now().UTC().Add(-age))
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

func deliveryAttempts(t *testing.T, pub types.PublicID) int {
	t.Helper()
	var attempts int
	require.NoError(t, testDB.QueryRow(
		`SELECT attempts FROM webhook_deliveries WHERE public_id = ?`, pub).Scan(&attempts))
	return attempts
}

// testStrandedAfter is the threshold these tests give the reaper.
//
// The reaper is deliberately instance-wide: in production it has to find
// rows abandoned by a worker that is no longer running, and those rows
// belong to whichever tenants that worker was serving. A test that runs
// it therefore acts on the whole table, including rows other tests are
// in the middle of delivering.
//
// Half an hour keeps that harmless. The seeded rows are backdated an
// hour so they are still caught, while no row a concurrently running
// test created can possibly be old enough — the suite does not run for
// thirty minutes. A one-minute threshold looked equivalent and was not:
// under -parallel 32 a delivery another test had claimed could sit in
// 'delivering' past a minute, and this reaper would rescue it out from
// under that test.
const testStrandedAfter = 30 * time.Minute

// runReaper executes one instance-wide stranded-delivery pass with the
// shared test threshold.
func runReaper(t *testing.T) {
	t.Helper()
	worker := webhook.NewWorker(testDB, generated.New(testDB))
	worker.StrandedAfter = testStrandedAfter
	worker.RequeueStrandedForTest(context.Background())
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

	stranded := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, time.Hour, 0)
	fresh := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, 0, 0)

	runReaper(t)

	// The claim is what the assertions are about, and the claim is "the
	// row left the stranded state", not "it holds this exact status
	// now". Anything may legitimately move a rescued row further along —
	// it is claimable the moment it is rescued — so pinning the string
	// would be asserting that nothing else in the instance did its job.
	//
	// attempts is the monotone witness: nothing decrements it, and the
	// only things that raise it are the rescue and a real delivery
	// attempt, both of which mean the row is no longer stranded.
	require.GreaterOrEqual(t, deliveryAttempts(t, stranded), 1,
		"a delivery abandoned in 'delivering' must be rescued back into the retry queue")
	require.NotEqual(t, "delivered", deliveryStatus(t, stranded),
		"the rescue must not mark an attempt that never completed as delivered")

	// A row claimed moments ago is still being delivered by whoever
	// claimed it. Requeueing that one would send the same POST twice for
	// a request about to succeed, which is why the threshold exists.
	// Stated through attempts for the same reason as above: it is the
	// quantity the reaper would move, and nothing else in the suite
	// touches a row this young.
	require.Zero(t, deliveryAttempts(t, fresh),
		"a delivery still within the threshold must be left alone")
	require.Equal(t, "delivering", deliveryStatus(t, fresh),
		"a delivery still within the threshold must keep its claim")
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

	// Seeded one attempt short of the cap, in a single INSERT: the next
	// strand has to exhaust it.
	const maxAttempts = 6
	pub := seedDeliveringRow(t, tt.WorkspacePublicID, created.Webhook.ID, time.Hour, maxAttempts-1)

	runReaper(t)

	// Whether this pass or a concurrently running test's pass rescued
	// the row does not matter — the reaper is instance-wide, and both
	// produce the same outcome. What matters is the outcome: the last
	// attempt is spent, and the row is out of the queue for good.
	require.Equal(t, maxAttempts, deliveryAttempts(t, pub),
		"the strand must spend the last attempt")

	var claimable int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM webhook_deliveries
		WHERE public_id = ? AND status IN ('pending','failed')
		  AND next_retry_at <= NOW() AND attempts < max_attempts AND enabled = TRUE`,
		pub).Scan(&claimable))
	require.Zero(t, claimable,
		"a delivery that spent its budget on strands must not be claimed again")
}
