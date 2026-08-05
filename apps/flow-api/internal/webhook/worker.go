// Package webhook implements the background webhook delivery worker. It
// subscribes to eventbus events via [Worker.Hook], creates pending
// delivery rows for matching subscriptions, and periodically processes
// those deliveries with HMAC-SHA256 signed POST requests. Each batch is
// first claimed atomically (FOR UPDATE SKIP LOCKED + status flip to
// 'delivering') so multiple worker replicas partition the queue instead
// of each delivering every row. Failed deliveries are retried with
// exponential backoff up to six attempts.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// retryDelays defines the exponential backoff schedule. After exhausting
// all slots the delivery is marked dead.
var retryDelays = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
}

// pollInterval is the ticker period for the delivery loop.
const pollInterval = 5 * time.Second

// batchSize is the maximum number of pending deliveries fetched per tick.
const batchSize = 50

// maxResponseBodyLen is the maximum number of bytes stored from the
// target's response body (for debugging failed deliveries).
const maxResponseBodyLen = 4096

// Worker is the background webhook delivery processor.
type Worker struct {
	db      *sql.DB
	queries *generated.Queries
	client  *http.Client
	done    chan struct{}

	// run is the function executed inside each Hook goroutine. Tests
	// override it to exercise the goroutine plumbing (detached cancel,
	// panic recovery) without a live database. Production code leaves
	// this nil and the hook routes to [Worker.createDeliveries].
	run func(ctx context.Context, workspaceID uint32, eventType string)
}

// NewWorker creates a Worker backed by the given database.
func NewWorker(db *sql.DB, q *generated.Queries) *Worker {
	return &Worker{
		db:      db,
		queries: q,
		client:  NewSafeClient(10 * time.Second),
		done:    make(chan struct{}),
	}
}

// Hook returns an eventbus.NotifyHook that creates webhook delivery
// rows for every active subscription whose event_types list matches the
// fired event. The returned function is non-blocking: it spawns a
// goroutine so the eventbus append path is never delayed.
//
// eventInternalID is the events.id of the row that triggered this
// fan-out; the worker resolves it to events.public_id and the row's
// occurred_at, then stamps both onto each webhook_deliveries row so
// repeated dispatches of the same event collapse to a single delivery
// per subscription via the (subscription_id, event_public_id) unique
// key, and the payload's OccurredAt reflects the event's logical time
// rather than the dispatch instant.
//
// The spawned goroutine inherits the parent context's values via
// [context.WithoutCancel] so trace span / logger attributes survive
// request cancellation while DB inserts still get to finish. A
// recover() guards the goroutine: a panic inside createDeliveries
// would otherwise crash the whole flow-api process.
func (w *Worker) Hook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		detached := context.WithoutCancel(ctx)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(detached, "webhook hook panic",
						slog.Any("recover", r),
						slog.Uint64("workspace_id", uint64(workspaceID)),
						slog.String("event_type", eventType))
				}
			}()
			if w.run != nil {
				w.run(detached, workspaceID, eventType)
				return
			}
			w.createDeliveries(detached, workspaceID, eventType, eventInternalID)
		}()
	}
}

// createDeliveries finds active subscriptions for the workspace, filters
// by event type, and inserts a pending delivery row for each match.
// eventInternalID is resolved once to (event_public_id, occurred_at) so
// every delivery row in this fan-out shares the same dedupe key and the
// payload's OccurredAt is the event's logical time, not the dispatch time.
func (w *Worker) createDeliveries(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	subs, err := w.queries.ListActiveSubscriptionsForEvent(ctx, workspaceID)
	if err != nil {
		slog.Warn("webhook: failed to list active subscriptions",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_type", eventType),
			slog.String("err", err.Error()))
		return
	}

	eventRow, err := w.queries.GetEventPublicIDAndOccurredAt(ctx, generated.GetEventPublicIDAndOccurredAtParams{
		WorkspaceID: workspaceID,
		ID:          eventInternalID,
	})
	if err != nil {
		slog.Warn("webhook: failed to resolve event public id",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("event_type", eventType),
			slog.Uint64("event_internal_id", eventInternalID),
			slog.String("err", err.Error()))
		return
	}
	eventPubID := eventRow.PublicID
	occurredAt := eventRow.OccurredAt

	payload := w.buildPayload(ctx, workspaceID, eventType, occurredAt)

	for _, sub := range subs {
		if !matchesEventType(sub.EventTypes, eventType) {
			continue
		}

		pubID := types.New()
		now := time.Now().UTC()
		if _, err := w.queries.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
			PublicID:       pubID,
			WorkspaceID:    workspaceID,
			SubscriptionID: sub.ID,
			EventType:      eventType,
			EventPublicID:  &eventPubID,
			PayloadJson:    payload,
			NextRetryAt:    sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			slog.Warn("webhook: failed to create delivery",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.String("event_type", eventType),
				slog.String("subscription", sub.PublicID.String()),
				slog.String("err", err.Error()))
		}
	}
}

// matchesEventType checks whether eventType appears in the subscription's
// event_types JSON array. A wildcard entry "*" matches everything.
func matchesEventType(raw json.RawMessage, eventType string) bool {
	if len(raw) == 0 {
		return false
	}
	var patterns []string
	if err := json.Unmarshal(raw, &patterns); err != nil {
		return false
	}
	for _, p := range patterns {
		if p == "*" || p == eventType {
			return true
		}
	}
	return false
}

// buildPayload constructs the JSON payload sent to the webhook target.
// occurredAt comes from the source events row so the payload reflects
// when the event happened, not when the worker happened to dispatch it.
func (w *Worker) buildPayload(ctx context.Context, workspaceID uint32, eventType string, occurredAt time.Time) json.RawMessage {
	type webhookPayload struct {
		EventType   string `json:"eventType"`
		WorkspaceID string `json:"workspaceId,omitempty"`
		OccurredAt  int64  `json:"occurredAt"`
	}
	// Look up the workspace public id for the payload.
	var wsPublicID string
	const q = `SELECT HEX(public_id) FROM workspaces WHERE id = ? LIMIT 1`
	var hexStr string
	if err := w.db.QueryRowContext(ctx, q, workspaceID).Scan(&hexStr); err == nil {
		// Convert hex to UUID format.
		if parsed, perr := types.Parse(
			hexStr[:8] + "-" + hexStr[8:12] + "-" + hexStr[12:16] + "-" + hexStr[16:20] + "-" + hexStr[20:],
		); perr == nil {
			wsPublicID = parsed.String()
		}
	}

	p := webhookPayload{
		EventType:   eventType,
		WorkspaceID: wsPublicID,
		OccurredAt:  occurredAt.Unix(),
	}
	b, err := json.Marshal(p)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// Start launches the periodic delivery processing loop in a background
// goroutine. Call [Worker.Stop] to terminate it.
func (w *Worker) Start(ctx context.Context) {
	go w.loop(ctx)
	slog.Info("webhook worker started", slog.Duration("interval", pollInterval))
}

// Stop signals the delivery loop to exit.
func (w *Worker) Stop() {
	close(w.done)
	slog.Info("webhook worker stopped")
}

// loop is the main ticker that periodically processes pending deliveries.
func (w *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// ProcessOnce drains one batch of pending deliveries synchronously.
// Exported solely for e2e tests that need to advance the delivery
// state machine without running the 5-second background loop.
func (w *Worker) ProcessOnce(ctx context.Context) {
	w.processBatch(ctx)
}

// ProcessOnceForSubscription is the tightly-scoped variant of ProcessOnce
// used by parallel-safe e2e tests: it only delivers rows belonging to a
// single subscription, so concurrent tests cannot accidentally consume
// each other's pending rows. It runs the same claim-then-POST state
// machine as processBatch. Production callers must use [Worker.Start]
// or [Worker.ProcessOnce] instead.
func (w *Worker) ProcessOnceForSubscription(ctx context.Context, subscriptionID uint32) {
	claimed, err := w.claimForSubscription(ctx, subscriptionID)
	if err != nil {
		slog.Warn("webhook: ProcessOnceForSubscription: claim failed",
			slog.String("err", err.Error()))
		return
	}
	for _, row := range claimed {
		w.deliver(ctx, row)
	}
}

// claimForSubscription is the subscription-scoped mirror of claimBatch:
// same FOR UPDATE SKIP LOCKED select and 'delivering' flip inside one
// short transaction, restricted to a single subscription's rows.
func (w *Worker) claimForSubscription(ctx context.Context, subscriptionID uint32) ([]generated.ClaimPendingDeliveriesRow, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		SELECT d.id, d.public_id, d.workspace_id, d.subscription_id,
		       d.event_type, d.payload_json, d.attempts, d.max_attempts,
		       ws.url, ws.secret
		FROM webhook_deliveries d
		INNER JOIN webhook_subscriptions ws ON ws.id = d.subscription_id
		WHERE d.subscription_id = ?
		  AND d.status IN ('pending', 'failed')
		  AND d.next_retry_at <= NOW()
		  AND d.attempts < d.max_attempts
		  AND d.enabled = TRUE
		ORDER BY d.next_retry_at ASC, d.id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	`
	rows, err := tx.QueryContext(ctx, q, subscriptionID, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claimed []generated.ClaimPendingDeliveriesRow
	for rows.Next() {
		var r generated.ClaimPendingDeliveriesRow
		if err := rows.Scan(
			&r.ID, &r.PublicID, &r.WorkspaceID, &r.SubscriptionID,
			&r.EventType, &r.PayloadJson, &r.Attempts, &r.MaxAttempts,
			&r.Url, &r.Secret,
		); err != nil {
			return nil, err
		}
		claimed = append(claimed, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(claimed) == 0 {
		return nil, nil
	}

	ids := make([]uint32, len(claimed))
	for i, r := range claimed {
		ids[i] = r.ID
	}
	if err := w.queries.WithTx(tx).MarkDeliveriesClaimed(ctx, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

// processBatch atomically claims a batch of pending webhook deliveries
// and POSTs them. The claim commits before any network I/O, so with N
// worker replicas each due row is delivered by exactly one replica.
func (w *Worker) processBatch(ctx context.Context) {
	rows, err := w.claimBatch(ctx)
	if err != nil {
		slog.Warn("webhook: failed to claim pending deliveries",
			slog.String("err", err.Error()))
		return
	}

	for _, row := range rows {
		w.deliver(ctx, row)
	}
}

// claimBatch selects up to batchSize due deliveries with
// FOR UPDATE SKIP LOCKED and flips them to 'delivering' inside a single
// short transaction. Once committed the rows no longer match the claim
// query's status filter, so a concurrent replica scanning the queue
// skips them instead of double-delivering. The row locks are held only
// for the two statements — the HTTP POST happens after COMMIT. On any
// error the deferred rollback releases the locks and the rows stay
// pending/failed for the next tick.
func (w *Worker) claimBatch(ctx context.Context) ([]generated.ClaimPendingDeliveriesRow, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	qtx := w.queries.WithTx(tx)
	rows, err := qtx.ClaimPendingDeliveries(ctx, batchSize)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uint32, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	if err := qtx.MarkDeliveriesClaimed(ctx, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

// deliver performs the HTTP POST for a single claimed ('delivering')
// row and records the terminal outcome (delivered / failed / dead).
func (w *Worker) deliver(ctx context.Context, row generated.ClaimPendingDeliveriesRow) {
	payload := row.PayloadJson
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	// Re-check the stored URL before every attempt: subscriptions created
	// before the SSRF policy existed (or whose DNS now points at a
	// non-public address) must not be delivered. The safe client's dialer
	// Control re-checks the actual connect address as well, so a DNS
	// record that flips between this check and the connect (rebinding)
	// is still blocked.
	if err := ValidateURL(ctx, row.Url); err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("destination rejected: %v", err))
		return
	}

	signature := sign(row.Secret, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Url, bytes.NewReader(payload))
	if err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("request build error: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodate-Signature-256", signature)
	req.Header.Set("X-Nodate-Event", row.EventType)
	req.Header.Set("X-Nodate-Delivery", row.PublicID.String())

	resp, err := w.client.Do(req)
	if err != nil {
		w.markFailed(ctx, row, 0, fmt.Sprintf("network error: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyLen))
	bodyStr := string(body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := w.queries.MarkDeliveryDelivered(ctx, generated.MarkDeliveryDeliveredParams{
			HttpStatus:   sql.NullInt16{Int16: int16(resp.StatusCode), Valid: true},
			ResponseBody: sql.NullString{String: bodyStr, Valid: bodyStr != ""},
			ID:           row.ID,
		}); err != nil {
			slog.Warn("webhook: failed to mark delivery as delivered",
				slog.String("delivery", row.PublicID.String()),
				slog.String("err", err.Error()))
		}
		return
	}

	w.markFailed(ctx, row, resp.StatusCode, bodyStr)
}

// markFailed marks a delivery attempt as failed. If max retries are
// exhausted the delivery is marked dead.
func (w *Worker) markFailed(ctx context.Context, row generated.ClaimPendingDeliveriesRow, httpStatus int, responseBody string) {
	nextAttempt := int(row.Attempts) // attempts is pre-increment; current index
	httpSt := sql.NullInt16{}
	if httpStatus > 0 {
		httpSt = sql.NullInt16{Int16: int16(httpStatus), Valid: true} //#nosec G115 -- HTTP status codes are 1xx-5xx, well within int16
	}
	respBody := sql.NullString{}
	if responseBody != "" {
		respBody = sql.NullString{String: responseBody, Valid: true}
	}

	// After this attempt, total attempts = row.Attempts + 1.
	if nextAttempt >= len(retryDelays) || nextAttempt+1 >= int(row.MaxAttempts) {
		if err := w.queries.MarkDeliveryDead(ctx, generated.MarkDeliveryDeadParams{
			HttpStatus:   httpSt,
			ResponseBody: respBody,
			ID:           row.ID,
		}); err != nil {
			slog.Warn("webhook: failed to mark delivery as dead",
				slog.String("delivery", row.PublicID.String()),
				slog.String("err", err.Error()))
		}
		return
	}

	retryAt := time.Now().UTC().Add(retryDelays[nextAttempt])
	if err := w.queries.MarkDeliveryFailed(ctx, generated.MarkDeliveryFailedParams{
		HttpStatus:   httpSt,
		ResponseBody: respBody,
		NextRetryAt:  sql.NullTime{Time: retryAt, Valid: true},
		ID:           row.ID,
	}); err != nil {
		slog.Warn("webhook: failed to mark delivery as failed",
			slog.String("delivery", row.PublicID.String()),
			slog.String("err", err.Error()))
	}
}

// sign computes the HMAC-SHA256 signature for a webhook payload.
func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// GenerateSecret produces a cryptographically random 32-byte hex secret
// suitable for webhook subscription HMAC signing.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}
